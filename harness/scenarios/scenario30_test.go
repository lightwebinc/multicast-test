package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 30 — BRC-131 block announcement: basic delivery
//
// Mirrors scenarios/30-block-announce-delivery/run.sh.
//
// Sends block announcements via TCP to the proxy. All 3 listeners subscribe to
// GroupBlockBroadcast and must receive every frame regardless of shard/subtree filters.
func TestScenario30_BlockAnnounceDelivery(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s30")
	e.PatchEnv("s30-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	blockCount := 20
	expectedFrames := float64(blockCount * 2) // BlockAnnounce + CoinbaseTx per block

	e.Sleep(3*time.Second, "drain residual frames")

	beforeL := snapshotListeners(t, e, ctx, "s30")

	// Run send-block-announce.
	genCmd := []string{
		"send-block-announce",
		"-addr", "[fd10::2]:9002",
		"-blocks", "20",
		"-subtrees", "4",
		"-coinbase=true",
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
