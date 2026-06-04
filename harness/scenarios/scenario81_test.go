package scenarios

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Scenario 81 — collapsed-mesh full-duplex success demo (Phase 3)
//
// N independent collapsed nodes (real shard-proxy + shard-listener +
// retry-endpoint) in a full ip6gre mesh with mc-router, each with one connected
// miner. Real BSV frames are injected at every node's proxy via subtx-gen; the
// demo asserts every miner receives traffic ingested at every node — full
// duplex across the mesh, through the actual binaries.
//
// Drives the privileged netns demo (mesh/collapsed-mesh.sh) rather than the
// Docker driver: the default bridge + br_netfilter cannot carry the multicast
// mesh, but netns + ip6gre + smcroute can (proven by scenario 80). The binaries
// run host-native in each node's netns.
//
// Requirements (host): ip6_gre, NET_ADMIN/root, smcroute>=2.5, go (workspace),
// python3. Skipped unless MESH_DEMO=1 so unit CI stays unprivileged.
func TestScenario81_CollapsedMeshFullDuplex(t *testing.T) {
	if os.Getenv("MESH_DEMO") != "1" {
		t.Skip("set MESH_DEMO=1 (root, ip6_gre, smcroute>=2.5, go) to run the collapsed-mesh demo")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "mesh", "collapsed-mesh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := os.Getenv("MESH_NODES")
	if nodes == "" {
		nodes = "3"
	}
	cmd := exec.Command("bash", script, nodes)
	out, err := cmd.CombinedOutput()
	t.Logf("collapsed-mesh.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("collapsed-mesh demo failed: %v", err)
	}
}
