package scenarios

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"testing"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// doneLine matches subtx-gen's closing line, e.g.
// "done: sent=14116 late=63 errors=0 elapsed=15.002s avg_pps=941".
var doneLine = regexp.MustCompile(`done: sent=(\d+) late=(\d+)`)

// generatorLate returns the late gap fills subtx-gen reports on its closing
// "done:" line: every sequence number a -seq-gap-delay gap skipped, sent late
// in a frame of the same flow. The source container must have exited.
func generatorLate(t *testing.T, ctx context.Context, prefix string) float64 {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "logs", prefix+"-source").CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs %s-source: %v", prefix, err)
	}
	m := doneLine.FindSubmatch(out)
	if m == nil {
		t.Fatalf("%s-source: no \"done: sent=N late=N\" line in its output", prefix)
	}
	late, _ := strconv.ParseFloat(string(m[2]), 64)
	return late
}

// gapRepair is the recovery ledger of one run of the retryTopology: three
// listeners (listener1 unfiltered, listener2 and listener3 filtered) and
// retry1, fed by a generator that injects -seq-gap-delay gaps while netem
// loses frames on the listeners.
//
// Two things close a gap. A frame netem lost is in retry1's cache, so its
// NACK is answered with a retransmit. A sequence number the generator
// skipped is in no cache until its late fill is sent, and the late fill
// itself closes the gap at every listener that tracks the flow; a NACK sent
// before it gets a MISS. An ACK never closes a gap.
type gapRepair struct {
	detected, suppressed, unrecovered, lateFilled [3]float64
	retransmits                                   float64 // multicast and unicast copies retry1 sent
	hits, misses, rateDrops                       float64 // retry1's NACK outcomes, for the log
	late                                          float64 // late fills the generator sent
	flowShare                                     float64 // sum over the listeners of the share of flows each tracks
}

func newGapRepair(beforeL, afterL [3]map[string]float64, beforeR, afterR map[string]float64, late float64) gapRepair {
	var g gapRepair
	var delivered [3]float64
	for i := 0; i < 3; i++ {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		g.detected[i] = d["bsl_gaps_detected_total"]
		g.suppressed[i] = d["bsl_gaps_suppressed_total"]
		g.unrecovered[i] = d["bsl_gaps_unrecovered_total"]
		g.lateFilled[i] = d["bsl_gaps_late_filled_total"]
		delivered[i] = d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
	}
	dR := metrics.DeltaMap(beforeR, afterR)
	g.retransmits = dR["bre_retransmits_total"] + dR["bre_unicast_retransmits_total"]
	g.hits, g.misses, g.rateDrops = dR["bre_cache_hits_total"], dR["bre_cache_misses_total"], dR["bre_rate_limit_drops_total"]
	g.late = late
	// listener1 tracks every flow; the others a share of them, measured as
	// their deliveries over listener1's.
	if delivered[0] > 0 {
		for i := 0; i < 3; i++ {
			g.flowShare += delivered[i] / delivered[0]
		}
	}
	return g
}

func sum3(v [3]float64) float64 { return v[0] + v[1] + v[2] }

func (g gapRepair) log(t *testing.T) {
	t.Helper()
	for i := 0; i < 3; i++ {
		t.Logf("listener%d: detected=%.0f suppressed=%.0f unrecovered=%.0f late_filled=%.0f",
			i+1, g.detected[i], g.suppressed[i], g.unrecovered[i], g.lateFilled[i])
	}
	t.Logf("retry1: cache_hits=%.0f cache_misses=%.0f rate_limit_drops=%.0f retransmits=%.0f",
		g.hits, g.misses, g.rateDrops, g.retransmits)
	t.Logf("generator late fills=%.0f; flow share over the listeners=%.2f", g.late, g.flowShare)
}

// assertAccounted checks that every gap closed by a frame has one behind it:
// at most one gap per retransmit retry1 sent, plus at most one per late fill
// at each listener tracking the fill's flow. tol absorbs the flow-share
// estimate.
func (g gapRepair) assertAccounted(t *testing.T, tol float64) {
	t.Helper()
	bound := g.retransmits + g.late*g.flowShare*(1+tol)
	metrics.AssertLTE(t, "gaps suppressed <= retransmits + late fills at the listeners tracking them",
		sum3(g.suppressed), bound)
}

// assertNothingLost checks that no gap ended without its frame: a gap booked
// unrecovered counts only if no frame for it ever arrived (late_filled books
// the ones that did).
func (g gapRepair) assertNothingLost(t *testing.T) {
	t.Helper()
	metrics.AssertZero(t, "gaps unrecovered whose frame never arrived",
		sum3(g.unrecovered)-sum3(g.lateFilled))
}

// logGapOutcomes logs why each listener gave gaps up and at which tier retry1
// rate-limited NACKs. The containers are fresh per scenario, so the totals
// are this run's.
func logGapOutcomes(t *testing.T, e *env.Env, ctx context.Context, prefix string) {
	t.Helper()
	for _, n := range []string{"1", "2", "3"} {
		url := e.MetricsURL(ctx, prefix+"-listener"+n)
		line := ""
		for _, r := range []string{"retries", "ttl", "restart", "rejected", "no_recover"} {
			if v := metrics.LabelledValue(t, url, "bsl_gaps_abandoned_total", "reason", r); v > 0 {
				line += fmt.Sprintf(" %s=%.0f", r, v)
			}
		}
		t.Logf("listener%s gaps abandoned by reason:%s", n, line)
	}
	url := e.MetricsURL(ctx, prefix+"-retry1")
	line := ""
	for _, l := range []string{"ip", "chain", "sequence", "group"} {
		if v := metrics.LabelledValue(t, url, "bre_rate_limit_drops_total", "level", l); v > 0 {
			line += fmt.Sprintf(" %s=%.0f", l, v)
		}
	}
	t.Logf("retry1 rate-limit drops by level:%s", line)
}
