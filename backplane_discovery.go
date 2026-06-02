package surf

import (
	"context"
	"net"
	"strconv"
)

// This file provides the two Discoverers most clustered deployments need. A
// Discoverer only has to return current peer addresses; the membership layer
// handles liveness, so a stale or churning list is fine.

// staticPeers is a fixed peer list, suitable for ECS, VMs, or any environment
// where peers are known up front or supplied via configuration/env.
type staticPeers struct{ addrs []string }

// StaticPeers returns a Discoverer that always reports the given addresses
// ("host:port"). Include every instance's advertise address (a node ignores
// itself).
func StaticPeers(addrs ...string) Discoverer {
	return &staticPeers{addrs: append([]string(nil), addrs...)}
}

func (s *staticPeers) Peers(ctx context.Context) ([]string, error) {
	return append([]string(nil), s.addrs...), nil
}

// k8sHeadless discovers peers from a Kubernetes headless Service. A headless
// Service publishes one DNS A record per ready pod, so resolving its FQDN
// yields the current pod IPs, which scales automatically with the Deployment /
// StatefulSet.
type k8sHeadless struct {
	fqdn       string
	port       int
	lookupHost func(ctx context.Context, host string) ([]string, error)
}

// K8sHeadless returns a Discoverer for the headless Service named service in
// namespace, using the default cluster domain (cluster.local) and the given
// peer port. Create a headless Service (clusterIP: None) selecting your pods
// and expose the backplane port on it.
func K8sHeadless(service, namespace string, port int) Discoverer {
	return K8sHeadlessFQDN(service+"."+namespace+".svc.cluster.local", port)
}

// K8sHeadlessFQDN is like K8sHeadless but takes the Service's fully-qualified
// domain name directly, for clusters using a non-default DNS domain.
func K8sHeadlessFQDN(fqdn string, port int) Discoverer {
	return &k8sHeadless{
		fqdn: fqdn,
		port: port,
		lookupHost: func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		},
	}
}

func (k *k8sHeadless) Peers(ctx context.Context) ([]string, error) {
	ips, err := k.lookupHost(ctx, k.fqdn)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.JoinHostPort(ip, strconv.Itoa(k.port)))
	}
	return out, nil
}
