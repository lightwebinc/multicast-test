package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 31 — BRC-131 block announcement: NACK retransmission
//
// Mirrors scenarios/31-block-announce-retransmit/run.sh.
//
// 10% loss on listeners + block announcements. Retry endpoint caches the V4
// frames. Listeners detect gaps and NACK → retransmit fills them.
func TestScenario31_BlockAnnounceRetransmit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s31")
	e.PatchEnv("s31-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
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
		"send-block-announce",
		"-addr", "[fd10::2]:9002",
		"-blocks", "50",
		"-subtrees", "4",
		"-coinbase=true",
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

	deltaR := metrics.DeltaMap(beforeR, afterR)
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f retransmits=%.0f",
		gapsDetected, nacksDispatched, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retransmits", retransmits)
}
