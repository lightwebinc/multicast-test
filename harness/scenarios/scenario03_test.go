package scenarios

import (
	"testing"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 03 — Functional subtree filter
//
// Mirrors scenarios/03-functional-subtree-filter/run.sh.
//
// Same topology as scenario 01. Focuses on listener3 which has
// SUBTREE_INCLUDE set to one of 8 subtrees. Asserts that:
//   - forwarded ≈ received × 1/8
//   - dropped (subtree_include_miss) ≈ received × 7/8
func TestScenario03_FunctionalSubtreeFilter(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s03")
	genCmd := subtxGenCmd("[fd10::2]:9000")

	_, _, _, _, beforeL3, afterL3 :=
		runTrafficAndSnapshot(t, e, "s03", genCmd)

	deltaL3 := metrics.DeltaMap(beforeL3, afterL3)

	recvL3 := deltaL3["bsl_frames_received_total"]
	dropL3 := deltaL3["bsl_frames_dropped_total"]
	passL3 := recvL3 - dropL3

	t.Logf("listener3: received=%.0f dropped=%.0f passed=%.0f", recvL3, dropL3, passL3)

	// Subtree-include: passed ≈ received × 1/8 (±15%).
	metrics.AssertNear(t, "listener3 passed (subtree-include)", passL3, recvL3*1/8, 0.15)

	// Dropped ≈ received × 7/8 (±10%).
	metrics.AssertNear(t, "listener3 dropped subtree_include_miss", dropL3, recvL3*7/8, 0.10)

	// Sanity.
	metrics.AssertGTE(t, "listener3 received > 1000", recvL3, 1000)
}
