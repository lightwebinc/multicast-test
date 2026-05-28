package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 36 — BRC-134 anchor frame: basic delivery
//
// Mirrors scenarios/36-anchor-delivery/run.sh.
//
// Sends anchor frames via TCP. All listeners must receive and forward them
// on GroupBlockBroadcast (FF0E::B:FFFE).
func TestScenario36_AnchorDelivery(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s36")
	e.PatchEnv("s36-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	anchorCount := 20.0

	e.Sleep(3*time.Second, "drain residual")

	beforeL := snapshotListeners(t, e, ctx, "s36")

	genCmd := []string{
		"send-anchor-frame",
		"-tcp",
		"-addr", "[fd10::2]:9002",
		"-count", "20",
		"-interval", "100ms",
	}
	startGenerator(t, ctx, "s36", genCmd)
	waitGenerator(t, ctx, "s36")

	e.Sleep(3*time.Second, "pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s36")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]

		t.Logf("%s: brc134_received=%.0f", label, recv)

		metrics.AssertNear(t, label+" brc134 received ≈ expected", recv, anchorCount, 0.10)
	}
}
