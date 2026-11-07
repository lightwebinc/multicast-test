package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 93: fragmented BEEF under loss: fragment-level NACK recovery
//
// The missing composition of 95 (BEEF fragmentation) and 96 (BEEF NACK
// recovery): large objects leave the proxy as BRC-130 fragments
// (OrigFrameVer 0x09), 10% netem loss drops individual FRAGMENTS on the
// listeners, and recovery must operate at fragment granularity, the
// listeners feed every fragment to the gap tracker, NACK the missing SeqNums
// with a ZERO SubtreeID (the V9 flow contract, blocker B1), and the
// BEEF-enabled retry serves the cached fragments back. Reassembly then
// completes with the ContentID (SHA-256d) check armed, so a recovered object
// delivered with wrong bytes would fail loudly rather than pass silently.
//
// Tail losses (final fragments of the train, with nothing behind them to
// expose the gap) time out rather than recover, same as scenario 34, so
// completion asserts near-total delivery, not exactness, while every
// recovery counter must move.
func TestScenario93_BeefFragmentLossRecovery(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s93")
	e.PatchEnv("s93-proxy", map[string]string{
		"TCP_LISTEN_PORT": "9002",
		"FRAG_MTU":        "1280",
	})
	for _, l := range []string{"s93-listener1", "s93-listener2", "s93-listener3"} {
		e.PatchEnv(l, map[string]string{
			"BEEF_TOPICS":         "tm_s93",
			"VERIFY_PAYLOAD_HASH": "true", // arms the reassembly SHA-256d (= ContentID) check
		})
	}
	// The retry must join the band and cache V3 fragments (OrigFrameVer 0x09)
	// or every fragment NACK dies unanswered.
	e.PatchEnv("s93-retry1", map[string]string{"BEEF_ENABLED": "true"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	for _, l := range []string{"s93-listener1", "s93-listener2", "s93-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 10.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	const objects = 40.0
	beforeL := snapshotListeners(t, e, ctx, "s93")
	beforeR := e.Snapshot(ctx, "s93-retry1")
	beforeP := e.Snapshot(ctx, "s93-proxy")

	// 4 KiB objects at FRAG_MTU 1280 → 4 fragments each; 40 objects ≈ 160
	// fragments per listener, so 10% loss yields dozens of fragment gaps per
	// run, zero recoveries can never pass by luck.
	startGenerator(t, ctx, "s93", []string{
		"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s93",
		"-object-bytes", "4096", "-count", "40", "-interval", "80ms",
	})
	waitGenerator(t, ctx, "s93")
	e.Sleep(12*time.Second, "fragment NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s93")
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s93-retry1"))

	gaps := sumListenerDelta("s93", "bsl_gaps_detected_total", beforeL, afterL)
	nacks := sumListenerDelta("s93", "bsl_nacks_dispatched_total", beforeL, afterL)
	completed := sumListenerDelta("s93", "bsl_reassembly_completed_total", beforeL, afterL)
	mismatch := sumListenerDelta("s93", "bsl_reassembly_hash_mismatch_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	retransmits := deltaR["bre_retransmits_total"]
	cached := deltaR["bre_frames_cached_total"]

	t.Logf("listeners: completed=%.0f mismatch=%.0f gaps=%.0f nacks=%.0f",
		completed, mismatch, gaps, nacks)
	t.Logf("retry: cached=%.0f retransmits=%.0f", cached, retransmits)

	dp := metrics.DeltaMap(beforeP, metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s93-proxy")))
	t.Logf("proxy: objects=%.0f fragmented=%.0f fragments_emitted=%.0f",
		dp["bsp_beef_submissions_total"], dp["bsp_frames_fragmented_total"],
		dp["bsp_fragments_emitted_total"])
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		t.Logf("%s: started=%.0f completed=%.0f late_frags=%.0f abandoned=%.0f delivered=%.0f",
			label, d["bsl_reassembly_started_total"], d["bsl_reassembly_completed_total"],
			d["bsl_reassembly_late_fragments_total"], d["bsl_reassembly_abandoned_total"],
			d["bsl_frames_forwarded_total"]+d["bsl_egress_errors_total"])
	}

	// The recovery machinery must move at FRAGMENT granularity...
	metrics.AssertGT(t, "fragment gaps detected under loss", gaps)
	metrics.AssertGT(t, "fragment NACKs dispatched", nacks)
	metrics.AssertGT(t, "retry cached band frames", cached)
	metrics.AssertGT(t, "retry retransmits served", retransmits)
	// ...and convert into COMPLETED objects: at 10% fragment loss with no
	// recovery, P(all 4 fragments arrive) ≈ 0.9⁴ ≈ 0.656, so ~34% of
	// reassemblies would fail per listener, ≥90% of the 3×40 expected
	// completions is unreachable without working retransmission, while still
	// tolerating tail-loss timeouts.
	metrics.AssertNear(t, "objects reassembled across listeners", completed, 3*objects, 0.10)
	metrics.AssertZero(t, "recovered bytes verify (ContentID)", mismatch)

	// The ceiling half of that invariant, stated exactly rather than by
	// tolerance: NO listener may reassemble an object more than once.
	//
	// This is where the scenario used to fail (measured 230-241 against an
	// expected ~120) and the failure was real, not a mis-set expectation.
	//
	// retryEnv sets neither beacon flag, so the retry endpoint runs its BINARY
	// defaults: multicast repair on, unicast off. One listener's request is
	// therefore answered to the whole band and the other two members each
	// receive a fragment they already consumed. (The shipped ops posture is the
	// opposite, unicast-only, so this is the harness's default, not the
	// fabric's; the invariant below is the listener's to hold either way,
	// because which one applies is the responder's choice.)
	//
	// The listener used to open a fresh reassembly slot for that copy (the
	// live-slot duplicate check dies with the slot), which re-REASSEMBLED the
	// object, the pre-fix control run logged completed=95 against delivered=40
	// per listener, so the egress claim on (ContentID, TopicID) was catching the
	// duplicates before the wire; it is optional (-egress-dedup-local-cap 0) and
	// shorter-lived than the recovery horizon, so that is a cushion, not the
	// invariant. Worse, the never-fillable slot expired into an onIncomplete
	// NACK for fragments the listener already had, repair feeding repair.
	// Suppressing the late copy against a completion memory is the fix;
	// `late_frags > 0` keeps the ceiling from passing vacuously by proving the
	// copies really do arrive.
	late := sumListenerDelta("s93", "bsl_reassembly_late_fragments_total", beforeL, afterL)
	t.Logf("late repair copies suppressed across listeners: %.0f", late)
	metrics.AssertGT(t, "late repair copies reached non-losing listeners", late)
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		metrics.AssertLT(t, label+" reassembled each object at most once",
			d["bsl_reassembly_completed_total"], objects+1)
	}
}

// Scenario 93b: fragmented BEEF under loss, UNICAST-ONLY repair
//
// The variant above runs the retry on its BINARY defaults (multicast repair
// on, unicast off), which is NOT the shipped ops posture: the fabric sets
// beacon_flags_multicast=false, so every repair is a unicast reply to the
// requesting listener. Until this test existed that path had no coverage at
// all, the flags are set by no other scenario.
//
// It matters because unicast repair has a failure mode multicast does not: the
// reply is a socket read on the requester, so it only becomes a delivered
// frame if the listener re-injects it into the worker that owns the flow.
// A control run before shard-listener v1.20.0 booked every gap as recovered
// (bsl_gaps_unrecovered_total = 0) while completions sat at 22-30 of 40 per
// listener, the books balanced while the objects never finished, because
// reassembly state is per-worker and nothing registered the re-injection.
// v1.20.0 wires RegisterRecover per worker.
//
// The assertion that distinguishes fixed from broken is therefore COMPLETIONS,
// not recovery counters: a gap ledger that balances proves nothing on its own.
func TestScenario93b_BeefFragmentLossRecoveryUnicast(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s93b")
	e.PatchEnv("s93b-proxy", map[string]string{
		"TCP_LISTEN_PORT": "9002",
		"FRAG_MTU":        "1280",
	})
	for _, l := range []string{"s93b-listener1", "s93b-listener2", "s93b-listener3"} {
		e.PatchEnv(l, map[string]string{
			"BEEF_TOPICS":         "tm_s93b",
			"VERIFY_PAYLOAD_HASH": "true",
		})
	}
	// The shipped ops posture: repair is answered UNICAST to the requester
	// only. No band copy reaches a listener that did not ask.
	e.PatchEnv("s93b-retry1", map[string]string{
		"BEEF_ENABLED":           "true",
		"BEACON_FLAGS_MULTICAST": "false",
		"BEACON_FLAGS_UNICAST":   "true",
	})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	for _, l := range []string{"s93b-listener1", "s93b-listener2", "s93b-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 10.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	const objects = 40.0
	beforeL := snapshotListeners(t, e, ctx, "s93b")
	beforeR := e.Snapshot(ctx, "s93b-retry1")

	startGenerator(t, ctx, "s93b", []string{
		"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s93b",
		"-object-bytes", "4096", "-count", "40", "-interval", "80ms",
	})
	waitGenerator(t, ctx, "s93b")
	e.Sleep(12*time.Second, "fragment NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s93b")
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s93b-retry1"))

	gaps := sumListenerDelta("s93b", "bsl_gaps_detected_total", beforeL, afterL)
	nacks := sumListenerDelta("s93b", "bsl_nacks_dispatched_total", beforeL, afterL)
	completed := sumListenerDelta("s93b", "bsl_reassembly_completed_total", beforeL, afterL)
	mismatch := sumListenerDelta("s93b", "bsl_reassembly_hash_mismatch_total", beforeL, afterL)
	unrecovered := sumListenerDelta("s93b", "bsl_gaps_unrecovered_total", beforeL, afterL)
	late := sumListenerDelta("s93b", "bsl_reassembly_late_fragments_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	t.Logf("listeners: completed=%.0f/%.0f mismatch=%.0f gaps=%.0f nacks=%.0f unrecovered=%.0f late=%.0f",
		completed, 3*objects, mismatch, gaps, nacks, unrecovered, late)
	t.Logf("retry: cached=%.0f retransmits=%.0f unicast=%.0f",
		deltaR["bre_frames_cached_total"], deltaR["bre_retransmits_total"],
		deltaR["bre_unicast_retransmits_total"])
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		t.Logf("%s: started=%.0f completed=%.0f abandoned=%.0f",
			label, d["bsl_reassembly_started_total"],
			d["bsl_reassembly_completed_total"], d["bsl_reassembly_abandoned_total"])
	}

	// Repair must actually run, and it must be the UNICAST path.
	metrics.AssertGT(t, "fragment gaps detected under loss", gaps)
	metrics.AssertGT(t, "fragment NACKs dispatched", nacks)
	metrics.AssertGT(t, "retry served unicast retransmits", deltaR["bre_unicast_retransmits_total"])

	// THE C3 ASSERTION: recovered fragments must finish OBJECTS. This is what
	// the pre-v1.20.0 control run failed while its gap ledger looked perfect.
	metrics.AssertNear(t, "objects reassembled across listeners", completed, 3*objects, 0.10)
	metrics.AssertZero(t, "recovered bytes verify (ContentID)", mismatch)

	// Zero MULTICAST retransmits proves the posture: no repair was ever put on
	// the band, so a listener that did not ask received no copy.
	metrics.AssertZero(t, "no multicast retransmits under unicast-only repair", deltaR["bre_retransmits_total"])

	// Late fragments are still expected and must still be SUPPRESSED. They do
	// not come from band copies here (there are none) but from the requester's
	// own raced replies, a re-NACK or tail probe answered after the first copy
	// already completed the slot. The invariant is therefore the ceiling, not
	// absence: a late copy must never re-open a completed slot and reassemble
	// the object a second time.
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		metrics.AssertLT(t, label+" reassembled each object at most once",
			d["bsl_reassembly_completed_total"], objects+1)
	}
}
