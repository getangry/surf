package surf_test

import (
	"context"
	"fmt"
	"time"

	surf "github.com/getangry/surf"
)

// Sharing a session across instances with the typed KV helper. The default
// backend is in-process; swap in a cluster backend (see the cluster example)
// and the same code shares sessions across pods.
func ExampleNewKV() {
	app := surf.NewApp()
	defer app.Cleanup()

	type Session struct {
		UserID string
		Roles  []string
	}
	sessions := surf.NewKV[Session](app.Backplane(), "sess:")

	ctx := context.Background()
	_ = sessions.Set(ctx, "abc123", Session{UserID: "u-42", Roles: []string{"admin"}}, 30*time.Minute)

	s, ok, _ := sessions.Get(ctx, "abc123")
	fmt.Println(ok, s.UserID, s.Roles[0])
	// Output: true u-42 admin
}

// Acquiring an advisory lease for work that is merely wasteful to duplicate.
// A Lease is NOT a lock: it is not safe across a partition (see the Backplane
// docs), so never guard non-idempotent side effects with it.
func ExampleBackplane_lease() {
	app := surf.NewApp()
	defer app.Cleanup()
	bp := app.Backplane()
	ctx := context.Background()

	lease, err := bp.Lease(ctx, "refresh-the-cache", 15*time.Second)
	if err != nil {
		return
	}
	defer lease.Release(ctx)

	// ... do the duplicate-tolerant work while holding the lease ...
	fmt.Println(lease.Token() > 0)
	// Output: true
}

// Configuring a clustered, peer-to-peer backplane on Kubernetes. Pods find each
// other through a headless Service; all peer traffic and stored values are
// encrypted with the shared secret. No external datastore is involved.
func ExampleNewClusterBackplane() {
	secret := []byte("from-a-kubernetes-secret-32-bytes!")

	bp := surf.NewClusterBackplane(
		secret,
		surf.K8sHeadless("surf-headless", "default", 7946),
		surf.WithClusterBindAddr(":7946"),
	)

	app := surf.NewApp(surf.WithBackplane(bp))
	_ = app // app.Serve() starts the node; app.Cleanup() stops it.

	// For ECS / VMs / anywhere without Kubernetes DNS, use a static peer list:
	_ = surf.NewClusterBackplane(secret, surf.StaticPeers("10.0.0.1:7946", "10.0.0.2:7946"))
}

// Building an approximate distributed rate limit by dividing a global budget by
// the live instance count reported by ClusterSizer.
func ExampleClusterSizer() {
	app := surf.NewApp()
	defer app.Cleanup()

	const globalRatePerSec = 1000.0
	n := 1
	if cs, ok := app.Backplane().(surf.ClusterSizer); ok {
		n = cs.Size()
	}
	perInstance := globalRatePerSec / float64(n)
	fmt.Println(perInstance)
	// Output: 1000
}
