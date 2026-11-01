package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 31: BRC-131 block announcement: NACK retransmission
//
// Ported from the retired bash scenario 31.
//
// 10% loss on listeners + block announcements. Retry endpoint caches the V4
// frames. Listeners detect gaps and NACK → retransmit fills them.
func TestScenario31_BlockAnnounceRetransmit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s31")
	e.PatchEnv("s31-proxy", map[string]string{"BLOCK_LISTEN_PORT": "8727"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 10% loss on listeners.
	for _, l := range []string{"s31-listener1", "s31-listener2", "s31-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 10.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s31")
	beforeR := e.Snapshot(ctx, "s31-retry1")

	genCmd := []string{
		"send-block-push",
		"-addr", "[fd10::2]:8727",
		"-count", "50",
		"-subtrees", "4",
		"-interval", "50ms",
	}
	startGenerator(t, ctx, "s31", genCmd)
	waitGenerator(t, ctx, "s31")

	e.Sleep(10*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s31")
	urlR := e.MetricsURL(ctx, "s31-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	gapsDetected := sumListenerDelta("s31", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s31", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsSuppressed := sumListenerDelta("s31", "bsl_gaps_suppressed_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s31", "bsl_gaps_unrecovered_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f suppressed=%.0f unrecovered=%.0f retransmits=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed, gapsUnrecovered, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retransmits", retransmits)
	// Delivery-side evidence: a served retransmit must actually cancel a gap
	// at a listener, not just leave the retry endpoint.
	metrics.AssertGT(t, "gaps suppressed (repairs received)", gapsSuppressed)
}
