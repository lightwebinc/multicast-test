package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 25 — BRC-130 fragmentation: fragment loss / reassembly abandonment
//
// Mirrors scenarios/25-fragmentation-loss/run.sh.
//
// FRAG_MTU=1500, 2048-byte payload (2 fragments). 60% netem loss on listeners.
// With 60% drop rate, P(both arrive) = (0.4)^2 = 16%. Expect most reassemblies
// to be abandoned.
func TestScenario25_FragmentationLoss(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s25")
	e.PatchEnv("s25-proxy", map[string]string{"FRAG_MTU": "1500"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Snapshot before netem is applied so the HTTP metrics connection is reliable.
	beforeL1 := e.Snapshot(ctx, "s25-listener1")

	// 60% loss on all listeners (containers must be running).
	listeners := []string{"s25-listener1", "s25-listener2", "s25-listener3"}
	for _, l := range listeners {
		if err := env.ApplyNetemLoss(ctx, l, 60.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-payload-size", "2048")
	startGenerator(t, ctx, "s25", genCmd)
	waitGenerator(t, ctx, "s25")

	// Wait for reassembly TTL to expire on incomplete slots.
	e.Sleep(15*time.Second, "reassembly TTL eviction")

	// Remove netem before scraping so TCP metrics connections are reliable.
	// t.Cleanup will also attempt removal but handles the already-removed case.
	for _, l := range listeners {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}

	urlL1 := e.MetricsURL(ctx, "s25-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	started := deltaL1["bsl_reassembly_started_total"]
	completed := deltaL1["bsl_reassembly_completed_total"]
	abandoned := deltaL1["bsl_reassembly_abandoned_total"]

	t.Logf("listener1: started=%.0f completed=%.0f abandoned=%.0f",
		started, completed, abandoned)

	metrics.AssertGT(t, "reassembly started", started)
	metrics.AssertGT(t, "reassembly abandoned", abandoned)
	metrics.AssertLT(t, "completed < started", completed, started)
}
