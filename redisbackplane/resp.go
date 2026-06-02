package redisbackplane

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// This file is a minimal RESP2 client with a connection pool, written against
// the standard library only so the redisbackplane package adds no third-party
// dependency to surf. It implements just the commands the backplane needs
// (GET/SET/DEL and three EVAL scripts), not a general Redis client.

// resp is a parsed RESP2 reply.
type resp struct {
	typ   byte // '+' simple, '-' error, ':' int, '$' bulk, '*' array
	str   []byte
	num   int64
	arr   []resp
	isNil bool
}

// respClient is a pooled RESP2 connection set, implementing store via raw
// commands and Lua EVAL.
type respClient struct {
	addr     string
	password string
	db       int
	dial     time.Duration

	mu     sync.Mutex
	idle   []*respConn
	closed bool
	max    int
	open   int
}

type respConn struct {
	c net.Conn
	r *bufio.Reader
}

func newRespClient(addr, password string, db, poolSize int, dialTimeout time.Duration) *respClient {
	if poolSize <= 0 {
		poolSize = 8
	}
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	return &respClient{addr: addr, password: password, db: db, dial: dialTimeout, max: poolSize}
}

func (rc *respClient) getConn(ctx context.Context) (*respConn, error) {
	rc.mu.Lock()
	if rc.closed {
		rc.mu.Unlock()
		return nil, errors.New("redisbackplane: client closed")
	}
	if n := len(rc.idle); n > 0 {
		conn := rc.idle[n-1]
		rc.idle = rc.idle[:n-1]
		rc.mu.Unlock()
		return conn, nil
	}
	rc.mu.Unlock()
	return rc.dialConn(ctx)
}

func (rc *respClient) dialConn(ctx context.Context) (*respConn, error) {
	d := net.Dialer{Timeout: rc.dial}
	c, err := d.DialContext(ctx, "tcp", rc.addr)
	if err != nil {
		return nil, err
	}
	conn := &respConn{c: c, r: bufio.NewReader(c)}
	if rc.password != "" {
		if _, err := conn.do(ctx, [][]byte{[]byte("AUTH"), []byte(rc.password)}); err != nil {
			c.Close()
			return nil, err
		}
	}
	if rc.db != 0 {
		if _, err := conn.do(ctx, [][]byte{[]byte("SELECT"), []byte(strconv.Itoa(rc.db))}); err != nil {
			c.Close()
			return nil, err
		}
	}
	return conn, nil
}

func (rc *respClient) put(conn *respConn, healthy bool) {
	if !healthy {
		conn.c.Close()
		return
	}
	rc.mu.Lock()
	if rc.closed || len(rc.idle) >= rc.max {
		rc.mu.Unlock()
		conn.c.Close()
		return
	}
	rc.idle = append(rc.idle, conn)
	rc.mu.Unlock()
}

// do runs one command and returns its reply, recycling the connection.
func (rc *respClient) do(ctx context.Context, args ...[]byte) (resp, error) {
	conn, err := rc.getConn(ctx)
	if err != nil {
		return resp{}, err
	}
	r, err := conn.do(ctx, args)
	rc.put(conn, err == nil)
	return r, err
}

func (rc *respClient) close() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.closed = true
	for _, conn := range rc.idle {
		conn.c.Close()
	}
	rc.idle = nil
	return nil
}

func (conn *respConn) do(ctx context.Context, args [][]byte) (resp, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.c.SetDeadline(dl)
		defer conn.c.SetDeadline(time.Time{})
	}
	if err := writeCommand(conn.c, args); err != nil {
		return resp{}, err
	}
	return readReply(conn.r)
}

// writeCommand encodes args as a RESP array of bulk strings.
func writeCommand(c net.Conn, args [][]byte) error {
	var b []byte
	b = append(b, '*')
	b = strconv.AppendInt(b, int64(len(args)), 10)
	b = append(b, '\r', '\n')
	for _, a := range args {
		b = append(b, '$')
		b = strconv.AppendInt(b, int64(len(a)), 10)
		b = append(b, '\r', '\n')
		b = append(b, a...)
		b = append(b, '\r', '\n')
	}
	_, err := c.Write(b)
	return err
}

func readReply(r *bufio.Reader) (resp, error) {
	line, err := readLine(r)
	if err != nil {
		return resp{}, err
	}
	if len(line) == 0 {
		return resp{}, errors.New("redisbackplane: empty reply")
	}
	typ, rest := line[0], line[1:]
	switch typ {
	case '+':
		return resp{typ: typ, str: rest}, nil
	case '-':
		return resp{}, fmt.Errorf("redis: %s", string(rest))
	case ':':
		n, err := strconv.ParseInt(string(rest), 10, 64)
		if err != nil {
			return resp{}, err
		}
		return resp{typ: typ, num: n}, nil
	case '$':
		n, err := strconv.ParseInt(string(rest), 10, 64)
		if err != nil {
			return resp{}, err
		}
		if n < 0 {
			return resp{typ: typ, isNil: true}, nil
		}
		buf := make([]byte, n+2) // value + CRLF
		if _, err := readFull(r, buf); err != nil {
			return resp{}, err
		}
		return resp{typ: typ, str: buf[:n]}, nil
	case '*':
		n, err := strconv.ParseInt(string(rest), 10, 64)
		if err != nil {
			return resp{}, err
		}
		if n < 0 {
			return resp{typ: typ, isNil: true}, nil
		}
		arr := make([]resp, n)
		for i := range arr {
			arr[i], err = readReply(r)
			if err != nil {
				return resp{}, err
			}
		}
		return resp{typ: typ, arr: arr}, nil
	default:
		return resp{}, fmt.Errorf("redisbackplane: unknown reply type %q", typ)
	}
}

// readLine reads through the next CRLF and returns the content without it.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	// strip trailing \r\n (or \n)
	n := len(line)
	if n >= 2 && line[n-2] == '\r' {
		return line[:n-2], nil
	}
	return line[:n-1], nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
