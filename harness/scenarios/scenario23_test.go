package scenarios

import (
	"testing"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 23 — BRC-130 fragmentation: shard filter
//
// Mirrors scenarios/23-fragmentation-shard-filter/run.sh.
//
// Same as 22, but verifies that shard and subtree filters work correctly
// on fragmented frames after reassembly.
func TestScenario23_FragmentationShardFilter(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s23")
	e.PatchEnv("s23-proxy", map[string]string{"FRAG_MTU": "1500"})

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-payload-size", "2048")

	beforeL1, afterL1, beforeL2, afterL2, beforeL3, afterL3 :=
		runTrafficAndSnapshot(t, e, "s23", genCmd)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	deltaL2 := metrics.DeltaMap(beforeL2, afterL2)
	deltaL3 := metrics.DeltaMap(beforeL3, afterL3)

	startedL1 := deltaL1["bsl_reassembly_started_total"]
	completedL1 := deltaL1["bsl_reassembly_completed_total"]
	fwdL1 := deltaL1["bsl_frames_forwarded_total"]

	startedL2 := deltaL2["bsl_reassembly_started_total"]
	completedL2 := deltaL2["bsl_reassembly_completed_total"]
	fwdL2 := deltaL2["bsl_frames_forwarded_total"]

	completedL3 := deltaL3["bsl_reassembly_completed_total"]
	fwdL3 := deltaL3["bsl_frames_forwarded_total"]

	t.Logf("l1: started=%.0f completed=%.0f fwd=%.0f", startedL1, completedL1, fwdL1)
	t.Logf("l2: started=%.0f completed=%.0f fwd=%.0f", startedL2, completedL2, fwdL2)
	t.Logf("l3: completed=%.0f fwd=%.0f", completedL3, fwdL3)

	metrics.AssertNear(t, "l1 completed ≈ started", completedL1, startedL1, 0.10)
	metrics.AssertNear(t, "l1 fwd ≈ completed", fwdL1, completedL1, 0.10)
	// l2: SHARD_INCLUDE=0,1 → receives 2 of 4 groups (50% of transactions).
	metrics.AssertNear(t, "l2 started ≈ l1_started/2", startedL2, startedL1/2, 0.20)
	metrics.AssertNear(t, "l2 fwd ≈ completed × 7/8", fwdL2, completedL2*7/8, 0.15)
	metrics.AssertNear(t, "l3 fwd ≈ completed × 1/8", fwdL3, completedL3*1/8, 0.20)
}
