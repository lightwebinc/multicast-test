package scenarios

import (
	"testing"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 07 — Functional BRC-128 + BRC-124 coexistence
//
// Mirrors scenarios/07-functional-brc128-mixed/run.sh.
//
// Generator alternates BRC-124 (raw) and BRC-128 (EF) payloads via
// -payload-format mixed. Infrastructure is payload-opaque, so filter
// ratios from scenario 01 must hold and bad_frame drops must be 0.
func TestScenario07_FunctionalBRC128Mixed(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s07")
	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-payload-format", "mixed")

	beforeL1, afterL1, beforeL2, afterL2, beforeL3, afterL3 :=
		runTrafficAndSnapshot(t, e, "s07", genCmd)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	deltaL2 := metrics.DeltaMap(beforeL2, afterL2)
	deltaL3 := metrics.DeltaMap(beforeL3, afterL3)

	recvL1 := deltaL1["bsl_frames_received_total"]
	recvL2 := deltaL2["bsl_frames_received_total"]
	recvL3 := deltaL3["bsl_frames_received_total"]

	passL1 := recvL1 - deltaL1["bsl_frames_dropped_total"]
	passL2 := recvL2 - deltaL2["bsl_frames_dropped_total"]
	passL3 := recvL3 - deltaL3["bsl_frames_dropped_total"]

	t.Logf("listener1: received=%.0f passed=%.0f", recvL1, passL1)
	t.Logf("listener2: received=%.0f passed=%.0f", recvL2, passL2)
	t.Logf("listener3: received=%.0f passed=%.0f", recvL3, passL3)

	metrics.AssertNear(t, "listener1 passed filter (mixed)", passL1, recvL1, 0.05)
	metrics.AssertNear(t, "listener2 passed filter (mixed, subtree-exclude)", passL2, recvL2*7/8, 0.10)
	metrics.AssertNear(t, "listener3 passed filter (mixed, subtree-include)", passL3, recvL3*1/8, 0.15)

	// Mixed traffic must not produce bad_frame drops.
	metrics.AssertZero(t, "listener1 bad_frame=0", deltaL1["bsl_bad_frame_drops_total"])
	metrics.AssertZero(t, "listener2 bad_frame=0", deltaL2["bsl_bad_frame_drops_total"])
	metrics.AssertZero(t, "listener3 bad_frame=0", deltaL3["bsl_bad_frame_drops_total"])

	metrics.AssertGTE(t, "listener1 received > 5000", recvL1, 5000)
}
