package scenarios

import (
	"testing"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 22 — BRC-130 fragmentation: basic delivery
//
// Mirrors scenarios/22-fragmentation-delivery/run.sh.
//
// Proxy configured with FRAG_MTU=1500. Generator sends 2048-byte payloads
// (2 fragments per frame). Asserts reassembly completes with 0 abandoned.
func TestScenario22_FragmentationDelivery(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s22")
	e.PatchEnv("s22-proxy", map[string]string{"FRAG_MTU": "1500"})

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-payload-size", "2048")

	beforeL1, afterL1, _, _, _, _ :=
		runTrafficAndSnapshot(t, e, "s22", genCmd)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	started := deltaL1["bsl_reassembly_started_total"]
	completed := deltaL1["bsl_reassembly_completed_total"]
	abandoned := deltaL1["bsl_reassembly_abandoned_total"]
	fwd := deltaL1["bsl_frames_forwarded_total"]
	egrErr := deltaL1["bsl_egress_errors_total"]

	t.Logf("listener1: started=%.0f completed=%.0f abandoned=%.0f fwd=%.0f egrErr=%.0f",
		started, completed, abandoned, fwd, egrErr)

	metrics.AssertGT(t, "reassembly started", started)
	metrics.AssertZero(t, "reassembly abandoned", abandoned)
	metrics.AssertNear(t, "completed ≈ started", completed, started, 0.10)
	metrics.AssertNear(t, "forwarded+egrErr ≈ completed", fwd+egrErr, completed, 0.10)
}
