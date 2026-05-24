package scenarios

import "testing"

// Scenario 00 — Firewall rules validation
//
// Tests nftables rules specific to LXD VM systemd services.
// Not portable to Docker.
func TestScenario00_Firewall(t *testing.T) {
	t.Skip("Tests nftables rules specific to LXD VM systemd services — not portable to Docker")
}
