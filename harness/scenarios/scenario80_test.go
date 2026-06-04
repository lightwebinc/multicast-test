package scenarios

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Scenario 80 — ip6gre fabric mesh multicast replication (Phase 0)
//
// Validates that the integrated-infra mc-router full-mesh model replicates
// IPv6 multicast across point-to-point ip6gre tunnels: a frame emitted on one
// node's local segment reaches the listeners on every peer node (full duplex).
//
// This is the privileged proof of the Phase 0 thesis. It currently drives the
// standalone netns repro (mesh/ip6gre-mesh.sh) rather than the Docker driver:
// the collapsed-mesh container topology (3 nodes × proxy+listener+retry sharing
// a netns, with ip6gre tunnels + smcrouted between them) is the next harness
// build. The shell repro mirrors roles/mc-router/templates/smcroute.conf.j2
// exactly and reports each replication direction independently.
//
// Requirements (host): the ip6_gre kernel module, NET_ADMIN/root, smcroute>=2.5
// and python3. Skipped unless MESH_REPRO=1 so unit CI stays unprivileged.
func TestScenario80_IP6GREMeshReplication(t *testing.T) {
	if os.Getenv("MESH_REPRO") != "1" {
		t.Skip("set MESH_REPRO=1 (root, ip6_gre, smcroute>=2.5) to run the ip6gre mesh repro")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", "mesh", "ip6gre-mesh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := os.Getenv("MESH_NODES")
	if nodes == "" {
		nodes = "3"
	}
	cmd := exec.Command("bash", repo, nodes)
	out, err := cmd.CombinedOutput()
	t.Logf("ip6gre-mesh.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("mesh replication repro failed: %v", err)
	}
}
