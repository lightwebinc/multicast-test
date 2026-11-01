package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 98: plane independence: concurrent tx + BEEF traffic
//
// The transaction plane and the BEEF plane run through one proxy and one
// listener set simultaneously. Both deliver in full and no plane perturbs
// the other's sequencing: HashKeys are domain-tagged (banded groupIdx), so
// the interleaved planes produce zero gaps.
func TestScenario98_PlaneIndependence(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s98")
	e.PatchEnv("s98-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	for _, l := range []string{"s98-listener1", "s98-listener2", "s98-listener3"} {
		e.PatchEnv(l, map[string]string{
			"BEEF_TOPICS": "tm_s98",
			// Uniform tx-plane election (the standard topology gives l2/l3
			// shard/subtree filters; this scenario asserts full totals).
			"SHARD_INCLUDE":   "",
			"SUBTREE_INCLUDE": "",
			"SUBTREE_EXCLUDE": "",
		})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")
	e.Sleep(3*time.Second, "drain residual")

	txFrames, beefObjects := 500.0, 50.0
	beforeL := snapshotListeners(t, e, ctx, "s98")

	// Both planes concurrently: subtx-gen on the UDP tx port (100 pps for
	// 5 s), beef-gen on the TCP form of the same open port (50 objects over
	// ~2 s inside that window).
	startGenerator(t, ctx, "s98", []string{
		"-addr", "[fd10::2]:8725",
		"-shard-bits", "2",
		"-subtrees", "8",
		"-subtree-seed", "multicast-lab-bsv",
		"-pps", "100",
		"-duration", "5s",
		"-payload-size", "256",
	})
	drv := dockerdriver.New()
	if err := drv.Start(ctx, driver.NodeConfig{
		Name:  "s98-source2",
		Image: "beef-gen:harness",
		IPv6:  "fd10::4",
		Cmd: []string{
			"-addr", "[fd10::2]:9002", "-topics", "tm_s98",
			"-count", "50", "-interval", "40ms",
		},
		Role: driver.RoleGenerator,
	}); err != nil {
		t.Fatalf("start beef source: %v", err)
	}
	t.Cleanup(func() { drv.Stop(context.Background(), "s98-source2") }) //nolint:errcheck

	waitGenerator(t, ctx, "s98")
	exitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := drv.WaitExit(exitCtx, "s98-source2"); err != nil {
		t.Logf("beef source wait: %v", err)
	}
	e.Sleep(3*time.Second, "pipeline drain")
	afterL := scrapeListeners(t, e, ctx, "s98")

	total := txFrames + beefObjects
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]
		unrecovered := delta["bsl_gaps_unrecovered_total"]
		t.Logf("%s: received=%.0f forwarded=%.0f egrErr=%.0f unrecovered=%.0f", label, recv, fwd, egrErr, unrecovered)

		metrics.AssertNear(t, label+" both planes delivered", recv, total, 0.10)
		metrics.AssertNear(t, label+" forwarded+egrErr ≈ received", fwd+egrErr, recv, 0.10)
		metrics.AssertZero(t, label+" unrecovered gaps (no cross-plane sequencing damage)", unrecovered)
	}
}
