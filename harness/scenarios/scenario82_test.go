package scenarios

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Scenario 82 — WireGuard admin overlay (Phase 4)
//
// Validates the integrated-infra admin-overlay role: an N-node full-mesh
// WireGuard overlay on a separate address space over a direct-veth transport.
// Asserts the wg handshake + end-to-end reachability over the overlay in every
// direction — the encrypted admin plane that carries SSH instead of an exposed
// public port.
//
// Drives the privileged netns repro (mesh/admin-overlay.sh). Requirements
// (host): the wireguard kernel module, wireguard-tools, NET_ADMIN/root. Skipped
// unless ADMIN_OVERLAY=1 so unit CI stays unprivileged.
func TestScenario82_WireGuardAdminOverlay(t *testing.T) {
	if os.Getenv("ADMIN_OVERLAY") != "1" {
		t.Skip("set ADMIN_OVERLAY=1 (root, wireguard module + tools) to run the admin overlay repro")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "mesh", "admin-overlay.sh"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := os.Getenv("MESH_NODES")
	if nodes == "" {
		nodes = "3"
	}
	out, err := exec.Command("bash", script, nodes).CombinedOutput()
	t.Logf("admin-overlay.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("admin overlay repro failed: %v", err)
	}
}
