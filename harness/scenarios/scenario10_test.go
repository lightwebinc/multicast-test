package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 10: Single endpoint ACK (tightened)
//
// Ported from the retired bash scenario 10.
//
// Low PPS with infrequent gaps so every dispatched NACK should produce an ACK.
// Assertions are tighter than scenario 99: gaps_unrecovered must be 0.
//
// A second phase loses the repair copy itself. The retry endpoint repairs by
// multicast (its default) and answers ACK with the multicast flag, which says a
// copy was sent, not that it arrived (BRC-126 §ACK Response, "Repair
// confirmation"). In listener1's network namespace a run of 40 data frames from
// the proxy is dropped, then the first 40 copies the retry endpoint sends. A
// listener that closes a gap on that ACK books all 40 repaired and never
// receives them; one that waits for the frame asks again and is repaired by the
// second copy. Asserted on listener1, the one unfiltered listener and the only
// one that lost frames: the shortfall identity, delivered == sent, and at least
// two NACKs and two copies for each lost frame.
func TestScenario10_SingleEndpointACK(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s10")
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 1% loss on listeners.
	for _, l := range []string{"s10-listener1", "s10-listener2", "s10-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s10")
	beforeR := e.Snapshot(ctx, "s10-retry1")

	// No -seq-gap-* injection: injected seqnums are pre-stamped, PRESERVED by
	// the proxy, and never actually sent; each is a phantom permanent gap
	// that would trip the unrecovered==0 assert once its ~11s retry budget
	// expires (a latent flake this scenario carried until 2026-08). Netem is
	// the only loss source.
	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd,
		"-pps", "200",
		"-duration", "10s",
	)
	startGenerator(t, ctx, "s10", genCmd)
	waitGenerator(t, ctx, "s10")

	e.Sleep(3*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s10")
	urlR := e.MetricsURL(ctx, "s10-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s10-source")

	gapsDetected := sumListenerDelta("s10", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s10", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsSuppressed := sumListenerDelta("s10", "bsl_gaps_suppressed_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s10", "bsl_gaps_unrecovered_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nacksReceived := deltaR["bre_nack_requests_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f suppressed=%.0f unrecovered=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed, gapsUnrecovered)
	t.Logf("retry: nacks_received=%.0f retransmits=%.0f", nacksReceived, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "retransmits", retransmits)
	metrics.AssertGT(t, "gaps suppressed", gapsSuppressed)
	metrics.AssertZero(t, "gaps unrecovered", gapsUnrecovered)

	scenario10LostRepairCopy(t, e, ctx)
}

// scenario10LostRepairCopy is scenario 10's second phase: the repair copy of
// every lost frame is itself lost once.
func scenario10LostRepairCopy(t *testing.T, e *env.Env, ctx context.Context) {
	const (
		victim = "s10-listener1"
		lost   = 40
	)
	for _, l := range []string{"s10-listener1", "s10-listener2", "s10-listener3"} {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}
	e.Sleep(2*time.Second, "loss removed, quiet")
	beforeL := snapshotListeners(t, e, ctx, "s10")
	beforeP := e.Snapshot(ctx, "s10-proxy")
	beforeR := e.Snapshot(ctx, "s10-retry1")

	// The proxy's frames first, then the retry endpoint's copies: each rule
	// drops its own first `lost` datagrams to the data port.
	if err := env.ApplyBurstDropFrom(ctx, victim, "fd10::2", lost); err != nil {
		t.Fatalf("frame drop %s: %v", victim, err)
	}
	t.Cleanup(func() { env.RemoveBurstDrop(context.Background(), victim) }) //nolint:errcheck
	if err := env.ApplyBurstDropFrom(ctx, victim, "fd10::20", lost); err != nil {
		t.Fatalf("copy drop %s: %v", victim, err)
	}

	gen := subtxGenCmd("[fd10::2]:8725")
	gen = append(gen, "-pps", "200", "-duration", "10s")
	startGenerator(t, ctx, "s10", gen)
	waitGenerator(t, ctx, "s10")
	e.Sleep(5*time.Second, "repairs and re-asks")
	dropped, err := env.BurstDropped(ctx, victim)
	if err != nil {
		t.Fatalf("drop counters %s: %v", victim, err)
	}
	if err := env.RemoveBurstDrop(ctx, victim); err != nil {
		t.Fatalf("drop rule removal %s: %v", victim, err)
	}
	trail := subtxGenCmd("[fd10::2]:8725")
	trail = append(trail, "-pps", "200", "-duration", "3s")
	startGenerator(t, ctx, "s10", trail)
	waitGenerator(t, ctx, "s10")

	afterL := settleRecovery(t, e, ctx, "s10", beforeL, 60*time.Second)
	afterP := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s10-proxy"))
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s10-retry1"))
	sent := metrics.DeltaMap(beforeP, afterP)["bsp_packets_forwarded_total"]
	deltaR := metrics.DeltaMap(beforeR, afterR)
	v := metrics.DeltaMap(beforeL[0], afterL[0])
	t.Logf("lost repair copy: frames dropped=%d, copies dropped=%d at %s; proxy sent=%.0f", dropped[0], dropped[1], victim, sent)
	t.Logf("%s: fwd=%.0f det=%.0f sup=%.0f unrec=%.0f nacks=%.0f; retry: nacks=%.0f retransmits=%.0f",
		victim, v["bsl_frames_forwarded_total"], v["bsl_gaps_detected_total"], v["bsl_gaps_suppressed_total"],
		v["bsl_gaps_unrecovered_total"], v["bsl_nacks_dispatched_total"],
		deltaR["bre_nack_requests_total"], deltaR["bre_retransmits_total"])

	metrics.AssertEq(t, "proxy frames dropped at "+victim, float64(dropped[0]), lost)
	metrics.AssertEq(t, "repair copies dropped at "+victim, float64(dropped[1]), lost)
	assertRecoveryIdentity(t, "listener1 (lost repair copy)", beforeL[0], afterL[0], sent)
	delivered := v["bsl_frames_forwarded_total"] + v["bsl_egress_errors_total"]
	metrics.AssertEq(t, "listener1 delivered == sent (every lost copy asked for again)", delivered, sent)
	metrics.AssertGTE(t, "listener1 NACKs (each lost frame asked for twice)", v["bsl_nacks_dispatched_total"], 2*lost)
	metrics.AssertGTE(t, "retry multicast copies (two for each lost frame)", deltaR["bre_retransmits_total"], 2*lost)
}
