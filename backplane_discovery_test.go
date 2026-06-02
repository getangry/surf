package surf

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestStaticPeers(t *testing.T) {
	d := StaticPeers("a:1", "b:1")
	got, err := d.Peers(context.Background())
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a:1", "b:1"}) {
		t.Fatalf("got %v", got)
	}
	// Mutating the result must not affect the source.
	got[0] = "x"
	again, _ := d.Peers(context.Background())
	sort.Strings(again)
	if again[0] != "a:1" {
		t.Fatal("StaticPeers returned a mutable internal slice")
	}
}

func TestK8sHeadless_ResolvesPodIPs(t *testing.T) {
	k := &k8sHeadless{
		fqdn: "surf.default.svc.cluster.local",
		port: 7946,
		lookupHost: func(ctx context.Context, host string) ([]string, error) {
			if host != "surf.default.svc.cluster.local" {
				t.Errorf("unexpected host %q", host)
			}
			return []string{"10.0.0.1", "10.0.0.2"}, nil
		},
	}
	got, err := k.Peers(context.Background())
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	want := []string{"10.0.0.1:7946", "10.0.0.2:7946"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestK8sHeadless_PropagatesLookupError(t *testing.T) {
	boom := errors.New("nxdomain")
	k := &k8sHeadless{
		fqdn:       "x",
		port:       1,
		lookupHost: func(ctx context.Context, host string) ([]string, error) { return nil, boom },
	}
	if _, err := k.Peers(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestK8sHeadless_FQDNConstruction(t *testing.T) {
	d := K8sHeadless("surf", "prod", 9000).(*k8sHeadless)
	if d.fqdn != "surf.prod.svc.cluster.local" || d.port != 9000 {
		t.Fatalf("fqdn=%q port=%d", d.fqdn, d.port)
	}
}
