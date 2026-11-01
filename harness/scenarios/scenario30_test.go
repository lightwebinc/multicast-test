package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 30: BRC-131 block announcement: basic delivery
//
// Ported from the retired bash scenario 30.
//
// Sends block announcements via TCP to the proxy. All 3 listeners subscribe to
// GroupBlockBroadcast and must receive every frame regardless of shard/subtree filters.
func TestScenario30_BlockAnnounceDelivery(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s30")
	e.PatchEnv("s30-proxy", map[string]string{"BLOCK_LISTEN_PORT": "8727"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	blockCount := 20
	// One fabric block frame per block: the push lane carries the BRC-144
	// body verbatim (coinbase inline), no separate coinbase frame.
	expectedFrames := float64(blockCount)

	e.Sleep(3*time.Second, "drain residual frames")

	beforeL := snapshotListeners(t, e, ctx, "s30")

	// Submit BRC-144 block push objects up the tunnel-side lane (8727).
	genCmd := []string{
		"send-block-push",
		"-addr", "[fd10::2]:8727",
		"-count", "20",
		"-subtrees", "4",
		"-interval", "50ms",
	}
	startGenerator(t, ctx, "s30", genCmd)
	waitGenerator(t, ctx, "s30")

	e.Sleep(3*time.Second, "multicast pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s30")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]

		t.Logf("%s: brc131_received=%.0f forwarded=%.0f egrErr=%.0f", label, recv, fwd, egrErr)

		metrics.AssertNear(t, label+" brc131 received ≈ expected", recv, expectedFrames, 0.05)
		metrics.AssertNear(t, label+" forwarded+egrErr ≈ received", fwd+egrErr, recv, 0.10)
	}
}
