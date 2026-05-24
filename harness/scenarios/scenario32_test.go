package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 32 — BRC-132 subtree data: basic delivery
//
// Mirrors scenarios/32-subtree-data-delivery/run.sh.
//
// Sends inline BRC-132 SubtreeData frames via TCP. All listeners with
// SUBTREE_DATA_ENABLED=true must receive and forward every frame.
func TestScenario32_SubtreeDataDelivery(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s32")
	e.PatchEnv("s32-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	for _, l := range []string{"s32-listener1", "s32-listener2", "s32-listener3"} {
		e.PatchEnv(l, map[string]string{"SUBTREE_DATA_ENABLED": "true"})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	frameCount := 30.0

	e.Sleep(3*time.Second, "drain residual")

	beforeL := snapshotListeners(t, e, ctx, "s32")

	genCmd := []string{
		"send-subtree-data",
		"-addr", "[fd10::2]:9002",
		"-frames", "30",
		"-nodes", "8",
		"-msg-type", "hashes",
		"-interval", "50ms",
	}
	startGenerator(t, ctx, "s32", genCmd)
	waitGenerator(t, ctx, "s32")

	e.Sleep(3*time.Second, "pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s32")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]

		t.Logf("%s: brc132_received=%.0f forwarded=%.0f egrErr=%.0f", label, recv, fwd, egrErr)

		metrics.AssertNear(t, label+" brc132 received ≈ expected", recv, frameCount, 0.10)
		metrics.AssertNear(t, label+" forwarded+egrErr ≈ received", fwd+egrErr, recv, 0.10)
	}
}
