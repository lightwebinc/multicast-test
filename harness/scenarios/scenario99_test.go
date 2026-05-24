package scenarios

import "testing"

// Scenario 99 — NACK retransmit end-to-end
//
// Mirrors scenarios/99-nack-retransmit/run.sh.
// Requires retry endpoint wired into the topology (Phase 2b).
func TestScenario99_NACKRetransmit(t *testing.T) {
	t.Skip("Phase 2b: requires bitcoin-retry-endpoint in topology")
}
