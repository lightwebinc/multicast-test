package scenarios

import (
	"testing"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 02 — Functional shard filter
//
// Mirrors scenarios/02-functional-shard-filter/run.sh.
//
// Same topology as scenario 01. Asserts that MLD snooping + SHARD_INCLUDE=0,1
// causes listener2 to receive fewer frames than listener1 (shard filter active),
// and that subtree-exclude filtering still works within the shard-filtered set.
func TestScenario02_FunctionalShardFilter(t *testing.T) {
	e, _, _, _ := basicTopology(t, "s02")
	genCmd := subtxGenCmd("[fd10::2]:8725")

	beforeL1, afterL1, beforeL2, afterL2, _, _ :=
		runTrafficAndSnapshot(t, e, "s02", genCmd)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	deltaL2 := metrics.DeltaMap(beforeL2, afterL2)

	recvL1 := deltaL1["bsl_frames_received_total"]
	recvL2 := deltaL2["bsl_frames_received_total"]
	dropL2 := deltaL2["bsl_frames_dropped_total"]
	passL2 := recvL2 - dropL2

	t.Logf("listener1: received=%.0f", recvL1)
	t.Logf("listener2: received=%.0f dropped=%.0f passed=%.0f", recvL2, dropL2, passL2)

	// Shard filter active: l2 must receive < 90% of l1.
	metrics.AssertLT(t, "shard filter active (l2 < 90% of l1)", recvL2, recvL1*0.90)

	// Subtree-exclude within shard set: dropped ≈ received/8 (±20%).
	metrics.AssertNear(t, "listener2 dropped subtree_exclude", dropL2, recvL2/8, 0.20)

	// Forwarded ≈ received × 7/8 (±10%).
	metrics.AssertNear(t, "listener2 passed filter", passL2, recvL2*7/8, 0.10)

	// Sanity.
	metrics.AssertGTE(t, "listener1 received > 5000", recvL1, 5000)
}
