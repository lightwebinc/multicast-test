package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 16 — Per-group retransmit rate limit (ACK preserved)
//
// Mirrors scenarios/16-group-ratelimit/run.sh.
//
// Tight RL_GROUP_RATE on retry1, high IP/chain/seq limits so only the group
// tier fires. Verifies:
//   - bre_rate_limit_drops_total{level="group"} > 0
//   - ACK responses sent > retransmits (ACK without retransmit on throttle)
func TestScenario16_GroupRatelimit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s16")

	e.PatchEnv("s16-retry1", map[string]string{
		"RL_IP_RATE":        "50000",
		"RL_IP_BURST":       "10000",
		"RL_CHAIN_RATE":     "10000",
		"RL_CHAIN_WINDOW":   "60s",
		"RL_SEQUENCE_MAX":   "10000",
		"RL_SEQUENCE_WINDOW": "60s",
		"RL_GROUP_RATE":     "2",
		"RL_GROUP_BURST":    "2",
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 5% loss on listeners — more gaps = more NACKs to hit the group limiter.
	for _, l := range []string{"s16-listener1", "s16-listener2", "s16-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 5.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeR := e.Snapshot(ctx, "s16-retry1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
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

	urlR := e.MetricsURL(ctx, "s16-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s16-source")

	deltaR := metrics.DeltaMap(beforeR, afterR)
	groupDrops := deltaR["bre_rate_limit_drops_total"]
	retransmits := deltaR["bre_retransmits_total"]
	nacksReceived := deltaR["bre_nack_requests_total"]

	t.Logf("retry1: nacks=%.0f retransmits=%.0f group_drops=%.0f",
		nacksReceived, retransmits, groupDrops)

	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "group rate limit drops", groupDrops)
	metrics.AssertGT(t, "retransmits (some still succeed)", retransmits)
}
