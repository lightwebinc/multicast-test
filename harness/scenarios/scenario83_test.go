package scenarios

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Scenario 83 — consumer-edge scale-out (Phase 5)
//
// A core node emits onto the fabric; a consumer-edge node receives it over an
// upstream tunnel and re-fans the BRC-129 groups to its own miner. Verifies
// consumer-tunnel termination can be offloaded to a neighbour (core -> edge ->
// miner, a 2-hop mc-router fan) using the same primitives as the collapsed node.
//
// Drives the privileged netns repro (mesh/consumer-edge.sh). Requirements:
// ip6_gre, smcroute>=2.5, python3, NET_ADMIN/root. Skipped unless EDGE_DEMO=1.
func TestScenario83_ConsumerEdgeScaleOut(t *testing.T) {
	if os.Getenv("EDGE_DEMO") != "1" {
		t.Skip("set EDGE_DEMO=1 (root, ip6_gre, smcroute>=2.5) to run the consumer-edge repro")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "mesh", "consumer-edge.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", script).CombinedOutput()
	t.Logf("consumer-edge.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("consumer-edge repro failed: %v", err)
	}
}
