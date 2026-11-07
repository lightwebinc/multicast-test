package scenarios

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 75: NACK repair across an INTER-FABRIC path
//
// Why this exists: every prior NACK proof (scenario 99) ran on an unshaped
// container veth at ~0.8ms RTT. Production, and the current standard ops posture,
// join regions over an interconnect at 60-160ms RTT, three orders of magnitude
// slower. Timing that repairs perfectly on a LAN can misbehave there for a reason
// that never appears unshaped: the retry interval is compared against the round
// trip, not against the loss rate.
//
// The hypothesis it was written to test was BackoffBase vs RTT: if a retry fires
// before the reply to the previous NACK lands, the listener burns its MaxRetries
// budget re-asking for frames already in flight. MEASUREMENT REFUTED THAT as a
// mechanism at any RTT an interconnect actually shows, and the refutation is the
// useful part of this scenario, so it is recorded rather than deleted: sendNACK
// drains the socket INLINE for respTimeout (300ms, not configurable) before the
// gap is ever rescheduled, so the next attempt cannot be issued until 300ms have
// passed no matter how small BackoffBase is. A 60-160ms round trip is absorbed
// whole. The race needs RTT > 300ms, half a second of round trip, which is not
// an interconnect, it is a satellite hop.
//
// What the rig CAN resolve, and what actually decides the defaults, is whether
// the slower inter-fabric timing COSTS anything: recovery must hold up in
// absolute terms, and must not fall below what LAN timing achieves on the same
// path. That is what the guards at the bottom assert. The load difference
// (NACKs and rate-limit drops at the retry endpoint) is logged, because it is
// the reason to prefer the slower ladder once recovery is shown to be equal.
//
// Shaping: netem is attached to the HOST-SIDE veth root qdisc, which delays and
// drops traffic INTO the container, the listener's ingress only. So the data
// path carries owDelayMs of latency and lossPct of loss, while the listener's
// NACK leaves unshaped and only the reply is delayed: a NACK exchange costs ONE
// leg, ~owDelayMs, not 2*owDelayMs. Naming it an 80ms RTT (as this scenario did)
// overstates it by 2x. Shaping the retry endpoint as well to close that gap was
// tried and rejected, see the note at the qdisc below; it is why the numbers
// here are quoted against owDelayMs, and why the header's claim about what the
// path costs is stated once, here, rather than implied by the constant's name.
func TestScenario75_NACKRepairInterFabric(t *testing.T) {
	const (
		owDelayMs = 40  // one-way, INGRESS ONLY: ~40ms per NACK exchange (see header)
		lossPct   = 3.0 // enough gaps to measure a ratio, not enough to swamp
		// Jitter and rate are set together, and both were reduced (8ms/1000pps)
		// because the pair had made this scenario BISTABLE, the single reason it
		// was flaky. netem jitter reorders whenever it exceeds the inter-packet
		// gap, and 8ms of jitter on a 1ms gap reordered constantly: runs recorded
		// ~12000 detected gaps against ~2700 real losses, so four fifths of the
		// "loss" this scenario measured was reordering. Each phantom gap is a
		// NACK the retry endpoint has to admit, and once that volume crossed the
		// endpoint's rate limit (RL_IP_RATE 100/s) the drops became retries and
		// the retries became more drops: three runs collapsed into that mode
		// (ratio 0.49-0.84, up to 34k rate-limit drops) while three stayed healthy
		// (ratio 0.95-0.99, <600 drops), and WHICH variant collapsed was random.
		// No threshold can be set across a bimodal distribution, the mode had to
		// go. 2ms of jitter on a 4ms gap leaves reordering present but occasional,
		// which is all this scenario needs; deliberate forward-jump coverage is
		// scenario 13's subject, not this one's.
		jitterMs = 2
		genPPS   = 250
	)

	variants := []struct {
		name  string
		short string
		env   map[string]string
	}{
		{"lan-tuned", "l", map[string]string{
			// What earlier downstream builds hardcoded before it was configurable.
			"NACK_JITTER_MAX":   "20ms",
			"NACK_BACKOFF_BASE": "200ms", // sub-RTT on paper; the inline drain hides it
			"NACK_BACKOFF_MAX":  "2s",
			"NACK_MAX_RETRIES":  "6",
		}},
		{"interfabric-tuned", "i", map[string]string{
			"NACK_JITTER_MAX":   "50ms",
			"NACK_BACKOFF_BASE": "400ms", // ~10x the shaped leg
			"NACK_BACKOFF_MAX":  "5s",
			"NACK_MAX_RETRIES":  "8",
		}},
	}

	ratio := make(map[string]float64)
	nacksPerGap := make(map[string]float64)
	inflight := make(map[string]float64)

	for _, v := range variants {
		prefix := "s75" + v.short
		t.Run(v.name, func(t *testing.T) {
			ctx := context.Background()
			e := retryTopologyNACKEnv(t, prefix, v.env)
			e.StartAll(ctx)
			e.Sleep(4*time.Second, "MLD querier settle")

			for _, sfx := range []string{"1", "2", "3"} {
				l := prefix + "-listener" + sfx
				if err := env.ApplyNetemWAN(ctx, l, owDelayMs, jitterMs, lossPct); err != nil {
					t.Fatalf("netem wan %s: %v", l, err)
				}
				name := l
				// Always restore, a scenario must never leave the rig shaped.
				t.Cleanup(func() { env.RemoveNetemLoss(ctx, name) }) //nolint:errcheck
			}
			// The retry endpoint is deliberately NOT shaped, and that is a
			// measured decision, not an oversight. Shaping its ingress too
			// (delay+jitter, no loss) was tried, to make the NACK exchange cost a
			// full round trip rather than one leg. It destroyed the instrument:
			// the endpoint also ingests the multicast data stream, so delaying
			// its ingress reordered the frames it caches AND bunched the NACK
			// arrivals into the per-group rate limiter. Rate-limit drops went
			// from ~500 to 2.6k-32k, dropped NACKs turned into retries, retries
			// into more drops, and the recovery ratio collapsed to 0.37-0.85 with
			// a spread far wider than anything it was trying to resolve. Three
			// runs, all unusable. Shaping only the listener keeps the loss on the
			// data path where the scenario wants it.

			beforeL := snapshotListeners(t, e, ctx, prefix)
			beforeR := e.Snapshot(ctx, prefix+"-retry1")

			gen := subtxGenCmd("[fd10::2]:8725")
			// Later flag wins, the -duration override on the same line is the
			// trick this scenario already relied on. 250pps over 30s is 7500 frames per
			// listener, ~225 real losses each: enough that the recovery ratio's
			// binomial noise is under a percentage point, few enough that the
			// NACK volume stays an order of magnitude below the retry endpoint's
			// admission limit rather than driving it.
			gen = append(gen, "-duration", "30s", "-pps", fmt.Sprint(genPPS))
			startGenerator(t, ctx, prefix, gen)
			waitGenerator(t, ctx, prefix)

			// Drain must exceed the worst-case retry ladder plus a full RTT, or
			// in-flight repairs get scored as unrecovered and the slower-backoff
			// variant is penalised for the harness's impatience.
			//
			// The ladder is (respTimeout 300ms + jittered backoff) per round,
			// backoff doubling from BackoffBase and capped at BackoffMax, for
			// MaxRetries rounds. Worst case: lan-tuned 6*0.3 + (0.2+0.4+0.8+1.6+2.0)
			// = 6.8s; interfabric-tuned 8*0.3 + (0.4+0.8+1.6+3.2+5+5+5) = 23.4s.
			// The 15s this used to wait sat BETWEEN the two, so the interfabric
			// variant was scraped mid-ladder and scored unrecovered for gaps it
			// was still repairing, a systematic bias toward the shorter backoff,
			// i.e. the exact artefact the comment above warns about.
			e.Sleep(30*time.Second, "NACK ladder (interfabric worst case 23.4s) + RTT drain")

			afterL := scrapeListeners(t, e, ctx, prefix)
			afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, prefix+"-retry1"))

			detected := sumListenerDelta(prefix, "bsl_gaps_detected_total", beforeL, afterL)
			suppressed := sumListenerDelta(prefix, "bsl_gaps_suppressed_total", beforeL, afterL)
			unrecovered := sumListenerDelta(prefix, "bsl_gaps_unrecovered_total", beforeL, afterL)
			deltaR := metrics.DeltaMap(beforeR, afterR)
			nacksRecv := deltaR["bre_nack_requests_total"]
			rtx := deltaR["bre_retransmits_total"]
			// detected - suppressed - unrecovered is what is STILL PENDING at
			// scrape: gaps the ladder had not finished. Non-zero means the drain
			// above is too short and the ratio is measuring harness patience.
			inflight[v.name] = detected - suppressed - unrecovered

			if detected == 0 {
				t.Fatalf("no gaps detected at %.1f%% loss, netem not applied?", lossPct)
			}
			r := suppressed / detected
			// NACKs the retry endpoint had to answer per gap. It sits WELL BELOW
			// 1 in the healthy regime (~0.3) because one NACK covers a seqnum
			// range while detected counts individual seqnums, so it is a load
			// figure, not a ratio to threshold. Climbing past ~1 is the signature
			// of the retry endpoint rate-limiting and the ladder re-asking.
			ampl := nacksRecv / detected

			t.Logf("%s: detected=%.0f suppressed=%.0f unrecovered=%.0f still_pending=%.0f ratio=%.3f retransmits=%.0f nacks_per_gap=%.2f",
				v.name, detected, suppressed, unrecovered, inflight[v.name], r, rtx, ampl)
			// Why a gap failed, separated at the responder: a cache MISS means
			// the frame was gone by the time the NACK arrived (the backoff
			// outran the retry TTL); a rate-limit drop means the ladder is
			// asking faster than the endpoint will answer. Without these the
			// ratio is a number with no cause attached.
			t.Logf("%s retry-endpoint: nacks=%.0f hits=%.0f misses=%.0f ratelimit_drops=%.0f responses=%.0f dedup=%.0f",
				v.name, nacksRecv, deltaR["bre_cache_hits_total"], deltaR["bre_cache_misses_total"],
				deltaR["bre_rate_limit_drops_total"], deltaR["bre_responses_sent_total"],
				deltaR["bre_retransmit_dedup_total"])

			ratio[v.name] = r
			nacksPerGap[v.name] = ampl

			metrics.AssertGT(t, "gaps suppressed (recovered)", suppressed)
		})
	}

	lan, lanOK := ratio["lan-tuned"]
	itf, itfOK := ratio["interfabric-tuned"]
	if !lanOK || !itfOK {
		t.Skip("a variant did not complete; nothing to compare")
	}
	t.Logf("shaped leg ~%dms: lan-tuned ratio=%.3f (%.2f nacks/gap) vs interfabric-tuned ratio=%.3f (%.2f nacks/gap)",
		owDelayMs, lan, nacksPerGap["lan-tuned"], itf, nacksPerGap["interfabric-tuned"])

	// TWO METRICS HAVE ALREADY BEEN TRIED HERE AS DISCRIMINATORS AND BOTH FAILED.
	// They are recorded so neither is proposed a third time:
	//
	//  1. NACK amplification (nacks-per-gap). Invalid by construction: one NACK
	//     covers a seqnum RANGE while gaps_detected counts individual missing
	//     seqnums, so the quotient sits near 0.3 whatever the timing.
	//  2. A SUPERIORITY guard on the recovery ratio (itf >= lan + 0.02). Not
	//     supported by the rig. Paired runs at the old settings: (lan 0.865, itf
	//     0.845), (0.906, 0.886), (0.975, 0.967), (0.964, 0.972), the two
	//     distributions sit on top of each other and the sign of the difference
	//     is not stable, so the guard failed on most runs and passed on some. It
	//     was not a tolerance that needed widening: the mechanism it assumed
	//     (sub-RTT backoff racing replies) cannot occur, because sendNACK drains
	//     the socket inline for respTimeout=300ms before any retry is scheduled
	//     and that swallows the whole round trip. See the header.
	//
	// What replaces it is the claim the defaults actually rest on, which the rig
	// DOES resolve: slower inter-fabric timing must not COST recovery.
	// Non-inferiority, not superiority.
	//
	// maxLoss is derived from measurement, not chosen: three paired runs at the
	// settings above gave (lan 1.000, itf 0.976), (0.993, 0.993), (0.971, 0.989)
	//, a worst observed |itf-lan| of 0.024, with each variant spanning 0.03
	// across runs. 0.08 is ~3x that, and still an order of magnitude tighter than
	// any regression worth catching: when the ladder genuinely fails it does not
	// slip a few points, it falls to 0.49-0.71 (the rate-limit collapse the
	// jitter/rate note above describes). A margin at 1x the noise is what made
	// this scenario flaky; do not tighten it back without new measurement.
	const maxLoss = 0.08
	if itf < lan-maxLoss {
		t.Errorf("inter-fabric timing (400ms base, 8 retries) repaired MATERIALLY LESS than LAN timing "+
			"(200ms base, 6 retries) over a %dms shaped leg: interfabric=%.3f lan=%.3f (floor %.3f). The slower "+
			"ladder is supposed to cost nothing in recovery; if it now does, re-derive the shipped "+
			"defaults rather than keeping them",
			owDelayMs, itf, lan, lan-maxLoss)
	}
	// And recovery must hold up outright, on both timings, the absolute claim
	// this scenario exists to keep true at inter-fabric RTT.
	for name, got := range map[string]float64{"interfabric-tuned": itf, "lan-tuned": lan} {
		if got < 0.80 {
			t.Errorf("%s repaired only %.1f%% of gaps over a %dms shaped leg / %.1f%% loss, too low",
				name, got*100, owDelayMs, lossPct)
		}
	}
	// The reason to prefer the slower ladder is LOAD, not recovery: it asks the
	// retry endpoint less often for the same repair (1.20-1.35 vs 1.23-1.25
	// nacks/gap across the three runs above, the direction is right but the
	// intervals overlap). Logged rather than guarded: three paired runs is not
	// enough to fix a threshold on, and the point of this block is that an
	// under-evidenced threshold is what put this scenario in the flaky column to
	// begin with.
	t.Logf("load at equal recovery: lan-tuned %.2f nacks/gap vs interfabric-tuned %.2f",
		nacksPerGap["lan-tuned"], nacksPerGap["interfabric-tuned"])
}
