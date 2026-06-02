package surf

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// This file is the security floor for the clustered backplane. All cluster
// members share one secret (from an environment variable / Kubernetes Secret).
// From it we derive, via HKDF, separate keys for two purposes that must never
// share key material:
//
//   - at-rest: every KV value is AES-256-GCM sealed before it leaves the
//     process, so a peer's stored copy (or a packet capture) is opaque.
//   - wire: every byte exchanged between peers travels inside an authenticated,
//     encrypted, replay-resistant frame.
//
// The shared secret implies a single trust domain: any holder of the secret is
// a fully trusted peer, and a leaked secret compromises the whole cluster.
// There is no per-peer identity (no PKI). This is an explicit, documented
// tradeoff in favor of "one secret to configure".
//
// An epoch byte travels with every sealed value and every connection so the
// secret can be rotated without a flag day: run with two epochs accepted, roll
// the deployment, then drop the old one.

const (
	cryptoMagic0     = 'S'
	cryptoMagic1     = 'B'
	cryptoVersion    = 1
	handshakeNonce   = 16
	maxFrameLen      = 16 << 20 // 16 MiB ceiling on a single wire frame
	maxNodeIDLen     = 255
	atRestNonceLen   = 12 // AES-GCM standard nonce
	derivedKeyLen    = 32 // AES-256
	hkdfInfoWire     = "surf/backplane/v1/wire"
	hkdfInfoAtRest   = "surf/backplane/v1/atrest"
	hkdfInfoConnSend = "surf/backplane/v1/conn/c2s"
	hkdfInfoConnRecv = "surf/backplane/v1/conn/s2c"
)

var confirmToken = []byte("surf-backplane-confirm-v1")

// Crypto-related sentinel errors.
var (
	ErrAuthFailed     = errors.New("surf: backplane peer authentication failed (wrong cluster secret?)")
	ErrUnknownEpoch   = errors.New("surf: backplane key epoch not known (rotate keys?)")
	errBadHandshake   = errors.New("surf: malformed backplane handshake")
	errFrameTooLarge  = errors.New("surf: backplane frame exceeds maximum size")
	errEmptySecret    = errors.New("surf: backplane cluster secret must not be empty")
	errDuplicateEpoch = errors.New("surf: duplicate backplane key epoch")
)

// epochKeys holds the keys derived for one secret epoch.
type epochKeys struct {
	epoch    byte
	wireKey  []byte      // base material for per-connection key derivation
	restAEAD cipher.AEAD // AES-256-GCM for at-rest value sealing
}

// keyring holds derived keys for one or more epochs. The current epoch is used
// to seal new values and to initiate new connections; any held epoch can open
// data, which is what makes rolling key rotation possible.
type keyring struct {
	byEpoch map[byte]*epochKeys
	current byte
}

// epochSecret pairs a rotation epoch with the secret bytes for that epoch.
type epochSecret struct {
	Epoch  byte
	Secret []byte
}

// newKeyring derives keys for the given secrets. The first entry is the current
// epoch (used for sealing/dialing). Pass more than one to accept an older epoch
// during rotation.
func newKeyring(secrets ...epochSecret) (*keyring, error) {
	if len(secrets) == 0 {
		return nil, errEmptySecret
	}
	kr := &keyring{byEpoch: make(map[byte]*epochKeys, len(secrets))}
	for i, s := range secrets {
		if len(s.Secret) == 0 {
			return nil, errEmptySecret
		}
		if _, dup := kr.byEpoch[s.Epoch]; dup {
			return nil, errDuplicateEpoch
		}
		wireKey, err := hkdf.Key(sha256.New, s.Secret, nil, hkdfInfoWire, derivedKeyLen)
		if err != nil {
			return nil, err
		}
		restKey, err := hkdf.Key(sha256.New, s.Secret, nil, hkdfInfoAtRest, derivedKeyLen)
		if err != nil {
			return nil, err
		}
		restAEAD, err := newGCM(restKey)
		if err != nil {
			return nil, err
		}
		kr.byEpoch[s.Epoch] = &epochKeys{epoch: s.Epoch, wireKey: wireKey, restAEAD: restAEAD}
		if i == 0 {
			kr.current = s.Epoch
		}
	}
	return kr, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealAtRest encrypts a KV value for storage/transit. Layout:
//
//	[epoch:1][nonce:12][ciphertext+tag]
//
// A fresh random nonce is used per value; values are sealed independently and
// in low enough volume per key that random 96-bit nonces are safe.
func (kr *keyring) sealAtRest(plaintext []byte) ([]byte, error) {
	ek := kr.byEpoch[kr.current]
	nonce := make([]byte, atRestNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 1+atRestNonceLen, 1+atRestNonceLen+len(plaintext)+ek.restAEAD.Overhead())
	out[0] = ek.epoch
	copy(out[1:], nonce)
	return ek.restAEAD.Seal(out, nonce, plaintext, nil), nil
}

// openAtRest reverses sealAtRest, selecting the key by the embedded epoch.
func (kr *keyring) openAtRest(blob []byte) ([]byte, error) {
	if len(blob) < 1+atRestNonceLen {
		return nil, errBadHandshake
	}
	ek, ok := kr.byEpoch[blob[0]]
	if !ok {
		return nil, ErrUnknownEpoch
	}
	nonce := blob[1 : 1+atRestNonceLen]
	ct := blob[1+atRestNonceLen:]
	pt, err := ek.restAEAD.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return pt, nil
}

// secureConn is an authenticated, encrypted, replay-resistant message stream
// over a duplex byte transport (typically a net.Conn). After the handshake,
// each direction has its own AES-256-GCM key derived from the shared epoch key
// and both peers' handshake nonces, so frames from one connection cannot be
// replayed onto another (the key differs) and frames cannot be replayed within
// a connection (the per-direction counter must advance in lockstep).
type secureConn struct {
	rw         io.ReadWriter
	sendAEAD   cipher.AEAD
	recvAEAD   cipher.AEAD
	sendCtr    uint64
	recvCtr    uint64
	PeerNodeID string
	Epoch      byte

	lenBuf [4]byte
}

// gcmNonceFromCounter renders a 96-bit GCM nonce from a frame counter. The key
// is unique per connection+direction, so a simple monotonic counter never
// repeats a (key, nonce) pair.
func gcmNonceFromCounter(ctr uint64) [12]byte {
	var n [12]byte
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n
}

// clientHandshake runs the dialing side of the handshake. nodeID identifies
// this instance to the peer.
func clientHandshake(rw io.ReadWriter, kr *keyring, nodeID string) (*secureConn, error) {
	return handshake(rw, kr, nodeID, true)
}

// serverHandshake runs the accepting side of the handshake.
func serverHandshake(rw io.ReadWriter, kr *keyring, nodeID string) (*secureConn, error) {
	return handshake(rw, kr, nodeID, false)
}

func handshake(rw io.ReadWriter, kr *keyring, nodeID string, isClient bool) (*secureConn, error) {
	if len(nodeID) > maxNodeIDLen {
		return nil, fmt.Errorf("surf: backplane nodeID too long (%d > %d)", len(nodeID), maxNodeIDLen)
	}
	myNonce := make([]byte, handshakeNonce)
	if _, err := rand.Read(myNonce); err != nil {
		return nil, err
	}

	// Send our hello.
	if err := writeHello(rw, kr.current, myNonce, nodeID); err != nil {
		return nil, err
	}
	// Read peer hello.
	peerEpoch, peerNonce, peerNodeID, err := readHello(rw)
	if err != nil {
		return nil, err
	}
	ek, ok := kr.byEpoch[peerEpoch]
	if !ok {
		return nil, ErrUnknownEpoch
	}

	// Both sides assemble the same salt = clientNonce||serverNonce, ordered by
	// role, then derive per-direction keys.
	var clientNonce, serverNonce []byte
	if isClient {
		clientNonce, serverNonce = myNonce, peerNonce
	} else {
		clientNonce, serverNonce = peerNonce, myNonce
	}
	salt := make([]byte, 0, 2*handshakeNonce)
	salt = append(salt, clientNonce...)
	salt = append(salt, serverNonce...)

	c2sKey, err := hkdf.Key(sha256.New, ek.wireKey, salt, hkdfInfoConnSend, derivedKeyLen)
	if err != nil {
		return nil, err
	}
	s2cKey, err := hkdf.Key(sha256.New, ek.wireKey, salt, hkdfInfoConnRecv, derivedKeyLen)
	if err != nil {
		return nil, err
	}
	c2sAEAD, err := newGCM(c2sKey)
	if err != nil {
		return nil, err
	}
	s2cAEAD, err := newGCM(s2cKey)
	if err != nil {
		return nil, err
	}

	sc := &secureConn{rw: rw, PeerNodeID: peerNodeID, Epoch: peerEpoch}
	if isClient {
		sc.sendAEAD, sc.recvAEAD = c2sAEAD, s2cAEAD
	} else {
		sc.sendAEAD, sc.recvAEAD = s2cAEAD, c2sAEAD
	}

	// Challenge-response: each side sends an encrypted confirmation. A wrong
	// secret produces different derived keys, so the peer's confirmation fails
	// to open and we reject the connection with ErrAuthFailed.
	if err := sc.WriteMsg(confirmToken); err != nil {
		return nil, err
	}
	got, err := sc.ReadMsg()
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(got, confirmToken) != 1 {
		return nil, ErrAuthFailed
	}
	return sc, nil
}

func writeHello(w io.Writer, epoch byte, nonce []byte, nodeID string) error {
	buf := make([]byte, 0, 4+len(nonce)+1+len(nodeID))
	buf = append(buf, cryptoMagic0, cryptoMagic1, cryptoVersion, epoch)
	buf = append(buf, nonce...)
	buf = append(buf, byte(len(nodeID)))
	buf = append(buf, nodeID...)
	_, err := w.Write(buf)
	return err
}

func readHello(r io.Reader) (epoch byte, nonce []byte, nodeID string, err error) {
	head := make([]byte, 4+handshakeNonce+1)
	if _, err = io.ReadFull(r, head); err != nil {
		return 0, nil, "", err
	}
	if head[0] != cryptoMagic0 || head[1] != cryptoMagic1 {
		return 0, nil, "", errBadHandshake
	}
	if head[2] != cryptoVersion {
		return 0, nil, "", fmt.Errorf("surf: unsupported backplane protocol version %d", head[2])
	}
	epoch = head[3]
	nonce = make([]byte, handshakeNonce)
	copy(nonce, head[4:4+handshakeNonce])
	idLen := int(head[4+handshakeNonce])
	id := make([]byte, idLen)
	if _, err = io.ReadFull(r, id); err != nil {
		return 0, nil, "", err
	}
	return epoch, nonce, string(id), nil
}

// WriteMsg seals and writes one length-prefixed frame.
func (sc *secureConn) WriteMsg(plaintext []byte) error {
	nonce := gcmNonceFromCounter(sc.sendCtr)
	sc.sendCtr++
	sealed := sc.sendAEAD.Seal(nil, nonce[:], plaintext, nil)
	if len(sealed) > maxFrameLen {
		return errFrameTooLarge
	}
	binary.BigEndian.PutUint32(sc.lenBuf[:], uint32(len(sealed)))
	if _, err := sc.rw.Write(sc.lenBuf[:]); err != nil {
		return err
	}
	_, err := sc.rw.Write(sealed)
	return err
}

// ReadMsg reads and opens one frame. A frame that fails to open (tampering,
// replay, or counter desync) returns ErrAuthFailed; the caller must drop the
// connection because the keystream is no longer in sync.
func (sc *secureConn) ReadMsg() ([]byte, error) {
	if _, err := io.ReadFull(sc.rw, sc.lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(sc.lenBuf[:])
	if n > maxFrameLen {
		return nil, errFrameTooLarge
	}
	sealed := make([]byte, n)
	if _, err := io.ReadFull(sc.rw, sealed); err != nil {
		return nil, err
	}
	nonce := gcmNonceFromCounter(sc.recvCtr)
	sc.recvCtr++
	pt, err := sc.recvAEAD.Open(nil, nonce[:], sealed, nil)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return pt, nil
}
