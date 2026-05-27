package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 33 — BRC-132 subtree data: fragmentation
//
// Mirrors scenarios/33-subtree-data-fragmentation/run.sh.
//
// Proxy with FRAG_MTU=1500. Large BRC-132 payloads (256 nodes × 32B = 8192B)
// are fragmented. Listeners reassemble and forward.
func TestScenario33_SubtreeDataFragmentation(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s33")
	e.PatchEnv("s33-proxy", map[string]string{
		"TCP_LISTEN_PORT": "9002",
		"FRAG_MTU":        "1500",
	})
	for _, l := range []string{"s33-listener1", "s33-listener2", "s33-listener3"} {
		e.PatchEnv(l, map[string]string{"SUBTREE_DATA_ENABLED": "true"})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	e.Sleep(3*time.Second, "drain residual")

	beforeL1 := snapshotListeners(t, e, ctx, "s33")

	genCmd := []string{
		"send-subtree-data",
		"-addr", "[fd10::2]:9002",
		"-frames", "20",
		"-nodes", "256",
		"-msg-type", "hashes",
		"-interval", "100ms",
	}
	startGenerator(t, ctx, "s33", genCmd)
	waitGenerator(t, ctx, "s33")

	e.Sleep(12*time.Second, "reassembly pipeline drain")

	afterL1 := scrapeListeners(t, e, ctx, "s33")

	delta := metrics.DeltaMap(beforeL1[0], afterL1[0])
	started := delta["bsl_reassembly_started_total"]
	completed := delta["bsl_reassembly_completed_total"]
	abandoned := delta["bsl_reassembly_abandoned_total"]

	t.Logf("listener1: started=%.0f completed=%.0f abandoned=%.0f",
		started, completed, abandoned)

	metrics.AssertGT(t, "reassembly started", started)
	metrics.AssertNear(t, "completed ≈ started", completed, started, 0.10)
	metrics.AssertZero(t, "reassembly abandoned", abandoned)
}
