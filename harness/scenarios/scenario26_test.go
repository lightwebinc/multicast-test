package scenarios

import (
	"testing"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 26 — BRC-130 fragmentation: high-throughput delivery ratio
//
// Mirrors scenarios/26-fragmentation-throughput/run.sh.
//
// FRAG_MTU=1500, 4096-byte payload (4 fragments), PPS=500 for 10s.
// Under no-loss conditions, ≥95% of reassemblies should complete.
func TestScenario26_FragmentationThroughput(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s26")
	e.PatchEnv("s26-proxy", map[string]string{"FRAG_MTU": "1500"})

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-payload-size", "4096", "-pps", "500", "-duration", "10s")

	beforeL1, afterL1, _, _, _, _ :=
		runTrafficAndSnapshot(t, e, "s26", genCmd)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	started := deltaL1["bsl_reassembly_started_total"]
	completed := deltaL1["bsl_reassembly_completed_total"]
	abandoned := deltaL1["bsl_reassembly_abandoned_total"]
	fwd := deltaL1["bsl_frames_forwarded_total"]
	egrErr := deltaL1["bsl_egress_errors_total"]

	t.Logf("listener1: started=%.0f completed=%.0f abandoned=%.0f fwd=%.0f egrErr=%.0f",
		started, completed, abandoned, fwd, egrErr)

	// ≥95% completion rate.
	minCompleted := started * 0.95
	metrics.AssertGTE(t, "completion rate ≥95%", completed, minCompleted)
	metrics.AssertZero(t, "reassembly abandoned", abandoned)
	metrics.AssertNear(t, "forwarded+egrErr ≈ completed", fwd+egrErr, completed, 0.10)
}
