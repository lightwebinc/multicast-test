package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 37 — BRC-134 anchor frame: NACK retransmission
//
// Mirrors scenarios/37-anchor-retransmit/run.sh.
//
// 10% loss on listeners + anchor frames. Retry endpoint caches V6 frames
// and retransmits to CtrlGroupControl on NACK.
func TestScenario37_AnchorRetransmit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s37")
	e.PatchEnv("s37-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 10% loss on listeners.
	for _, l := range []string{"s37-listener1", "s37-listener2", "s37-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 10.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s37")
	beforeR := e.Snapshot(ctx, "s37-retry1")

	genCmd := []string{
		"send-anchor-frame",
		"-tcp",
		"-addr", "[fd10::2]:9002",
		"-count", "50",
		"-interval", "50ms",
	}
	startGenerator(t, ctx, "s37", genCmd)
	waitGenerator(t, ctx, "s37")

	e.Sleep(10*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s37")
	urlR := e.MetricsURL(ctx, "s37-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	gapsDetected := sumListenerDelta("s37", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s37", "bsl_nacks_dispatched_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f retransmits=%.0f",
		gapsDetected, nacksDispatched, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retransmits", retransmits)
}
