package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 16: Per-group retransmit rate limit (no ACK on throttle)
//
// Ported from the retired bash scenario 16.
//
// Tight RL_GROUP_RATE on retry1, high IP/chain/seq limits so only the group
// tier fires. The group tier runs after the cache lookup, so only NACKs for
// frames retry1 holds (the netem losses) reach it; the generator's late gap
// fills are in no cache when their NACKs arrive. Verifies:
//   - bre_rate_limit_drops_total{level="group"} > 0
//   - some retransmits still go out, and no ACK is sent without one: a
//     throttled NACK gets no answer (RL_THROTTLE_RESPONSE is off), so the
//     listener keeps the gap and retries
//   - no more gaps close than the retransmits and late fills account for
func TestScenario16_GroupRatelimit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s16")

	e.PatchEnv("s16-retry1", map[string]string{
		"RL_IP_RATE":         "50000",
		"RL_IP_BURST":        "10000",
		"RL_CHAIN_RATE":      "10000",
		"RL_CHAIN_WINDOW":    "60s",
		"RL_SEQUENCE_MAX":    "10000",
		"RL_SEQUENCE_WINDOW": "60s",
		"RL_GROUP_RATE":      "2",
		"RL_GROUP_BURST":     "2",
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 5% loss on listeners: more gaps = more NACKs to hit the group limiter.
	for _, l := range []string{"s16-listener1", "s16-listener2", "s16-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 5.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s16")
	beforeR := e.Snapshot(ctx, "s16-retry1")
	urlR := e.MetricsURL(ctx, "s16-retry1")
	acksBefore := metrics.LabelledValue(t, urlR, "bre_responses_sent_total", "type", "ack")

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd,
		"-pps", "500",
		"-duration", "15s",
		"-seq-gap-every", "10",
		"-seq-gap-size", "3",
		"-seq-gap-delay", "500ms",
	)
	startGenerator(t, ctx, "s16", genCmd)
	waitGenerator(t, ctx, "s16")

	e.Sleep(15*time.Second, "NACK pipeline drain (listeners keep retrying)")

	afterL := scrapeListeners(t, e, ctx, "s16")
	afterR := metrics.ScrapeOrFail(t, urlR)
	acks := metrics.LabelledValue(t, urlR, "bre_responses_sent_total", "type", "ack") - acksBefore
	e.LogContainerOutput(ctx, "s16-source")

	late := generatorLate(t, ctx, "s16")
	g := newGapRepair(beforeL, afterL, beforeR, afterR, late)
	g.log(t)
	logGapOutcomes(t, e, ctx, "s16")

	deltaR := metrics.DeltaMap(beforeR, afterR)
	groupDrops := deltaR["bre_rate_limit_drops_total"]
	// An ACK goes with a multicast or a unicast retransmit, so both bound it.
	retransmits := deltaR["bre_retransmits_total"] + deltaR["bre_unicast_retransmits_total"]
	nacksReceived := deltaR["bre_nack_requests_total"]

	t.Logf("retry1: nacks=%.0f retransmits=%.0f acks=%.0f group_drops=%.0f",
		nacksReceived, retransmits, acks, groupDrops)

	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "group rate limit drops", groupDrops)
	metrics.AssertGT(t, "retransmits (some still succeed)", retransmits)
	metrics.AssertLTE(t, "ACKs sent <= retransmits (no ACK for a throttled NACK)", acks, retransmits)
	// Listener metrics do not say which frame closed a gap, so the repairs
	// that got through cannot be told apart from late fills; the ledger
	// still bounds the closures by the frames behind them.
	g.assertAccounted(t, 0.10)
}
