package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 96: BRC-148 NACK recovery on the BEEF plane
//
// BEEF flows are per (sender, group) with in-frame HashKey/SeqNum, so the
// standard BRC-126 machinery recovers plane loss unchanged: netem loss opens
// SeqNum gaps → listeners NACK → the BEEF-enabled retry endpoint serves the
// cached V9 frame and retransmits it to the TopicID-derived band group.
func TestScenario96_BeefNackRecovery(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s96")
	e.PatchEnv("s96-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	e.PatchEnv("s96-retry1", map[string]string{"BEEF_ENABLED": "true"})
	listeners := []string{"s96-listener1", "s96-listener2", "s96-listener3"}
	for _, l := range listeners {
		e.PatchEnv(l, map[string]string{"BEEF_TOPICS": "tm_s96"})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	for _, l := range listeners {
		if err := env.ApplyNetemLoss(ctx, l, 3.0); err != nil {
			t.Fatalf("apply netem loss on %s: %v", l, err)
		}
		l := l
		t.Cleanup(func() { env.RemoveNetemLoss(context.Background(), l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s96")
	beforeR := e.Snapshot(ctx, "s96-retry1")

	// One topic ⇒ one band group ⇒ one contiguous (sender, group) SeqNum
	// stream; gaps are unambiguous.
	startGenerator(t, ctx, "s96", []string{
		"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s96",
		"-count", "400", "-interval", "10ms",
	})
	waitGenerator(t, ctx, "s96")

	// Remove loss BEFORE the recovery drain so retransmits are not re-dropped.
	for _, l := range listeners {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}
	e.Sleep(7*time.Second, "NACK + retransmit recovery drain (loss removed)")

	afterL := scrapeListeners(t, e, ctx, "s96")
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s96-retry1"))

	gapsDetected := sumListenerDelta("s96", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s96", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s96", "bsl_gaps_unrecovered_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nackRequests := deltaR["bre_nack_requests_total"]
	cacheHits := deltaR["bre_cache_hits_total"]
	retransmits := deltaR["bre_retransmits_total"]
	framesCached := deltaR["bre_frames_cached_total"]

	recovered := gapsDetected - gapsUnrecovered
	t.Logf("gaps=%.0f nacks=%.0f unrecovered=%.0f | cached=%.0f nackReq=%.0f hits=%.0f retx=%.0f",
		gapsDetected, nacksDispatched, gapsUnrecovered, framesCached, nackRequests, cacheHits, retransmits)

	metrics.AssertGT(t, "retry cached BEEF frames (band joined)", framesCached)
	metrics.AssertGT(t, "listener gaps detected on the BEEF plane", gapsDetected)
	metrics.AssertGT(t, "listener NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retry NACK requests received", nackRequests)
	metrics.AssertGT(t, "retry cache hits (V9 frame found)", cacheHits)
	metrics.AssertGT(t, "retry BEEF retransmits", retransmits)
	metrics.AssertGT(t, "gaps recovered via BEEF retransmit", recovered)
}
