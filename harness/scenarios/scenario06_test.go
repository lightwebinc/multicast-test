package scenarios

import (
	"testing"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 06 — Functional BRC-128 (Extended Format payload)
//
// Mirrors scenarios/06-functional-brc128/run.sh.
//
// Same topology as scenario 01 but generator emits BRC-128 (EF) payloads via
// -payload-format brc128. Infrastructure is payload-agnostic, so all filter
// ratios from scenario 01 must hold. Additionally, bad_frame drops must be 0
// (EF payload does not change the BRC-124 frame header).
func TestScenario06_FunctionalBRC128(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s06")
	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-payload-format", "brc128")

	beforeL1, afterL1, beforeL2, afterL2, beforeL3, afterL3 :=
		runTrafficAndSnapshot(t, e, "s06", genCmd)

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

	// Same filter ratios as scenario 01.
	metrics.AssertNear(t, "listener1 passed filter", passL1, recvL1, 0.05)
	metrics.AssertNear(t, "listener2 passed filter (subtree-exclude)", passL2, recvL2*7/8, 0.10)
	metrics.AssertNear(t, "listener3 passed filter (subtree-include)", passL3, recvL3*1/8, 0.15)

	// EF payloads must NOT trip bad_frame counter.
	metrics.AssertZero(t, "listener1 bad_frame=0", deltaL1["bsl_bad_frame_drops_total"])
	metrics.AssertZero(t, "listener2 bad_frame=0", deltaL2["bsl_bad_frame_drops_total"])
	metrics.AssertZero(t, "listener3 bad_frame=0", deltaL3["bsl_bad_frame_drops_total"])

	metrics.AssertGTE(t, "listener1 received > 4000", recvL1, 4000)
}
