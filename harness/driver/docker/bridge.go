// Package docker implements the driver.Driver interface using the local Docker
// CLI. All commands are issued via os/exec wrapping "docker" and "ip".
package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// NetworkName is the Docker user-defined network used by all harness containers.
	NetworkName = "mcast-fabric"
	// BridgeName is the Linux bridge interface backing NetworkName.
	BridgeName = "brmcast0"
	// Subnet is the IPv6 prefix assigned to NetworkName.
	Subnet = "fd10::/64"
)

// CreateMcastBridge ensures the mcast-fabric Docker network exists and has MLD
// snooping + querier enabled on the backing bridge. Idempotent.
//
// The sysfs writes require elevated privileges. The function first tries direct
// writes; if permission is denied it retries via "sudo tee". If the calling
// process already has CAP_NET_ADMIN (e.g. running under sudo) the direct path
// succeeds.
//
// A 3-second settle delay is imposed after bridge creation so the MLD querier
// completes its first query cycle before containers start joining groups.
func CreateMcastBridge(ctx context.Context) error {
	// 1. Create the Docker network (idempotent — ignore "already exists").
	out, err := run(ctx, "docker", "network", "create",
		"--driver", "bridge",
		"--ipv6",
		"--subnet", Subnet,
		"--opt", "com.docker.network.bridge.name="+BridgeName,
		NetworkName,
	)
	if err != nil && !strings.Contains(out, "already exists") {
		return fmt.Errorf("docker network create: %w\n%s", err, out)
	}

	already := strings.Contains(out, "already exists")

	// 2. Wait briefly for the bridge interface to appear in the kernel.
	// Docker creates it asynchronously after returning from "network create".
	if err := waitForIface(ctx, BridgeName, 5*time.Second); err != nil {
		return fmt.Errorf("bridge interface %s did not appear: %w", BridgeName, err)
	}

	// 3. Enable MLD snooping and IPv6 querier using `ip link set` which works
	//    reliably as root without sysfs permission quirks.
	ipLinkSets := [][]string{
		{"link", "set", "dev", BridgeName, "type", "bridge", "mcast_snooping", "1"},
		{"link", "set", "dev", BridgeName, "type", "bridge", "mcast_querier", "1"},
	}
	for _, args := range ipLinkSets {
		if out2, err2 := run(ctx, "ip", args...); err2 != nil {
			fmt.Fprintf(os.Stderr, "[bridge] WARN ip %v: %v (%s)\n", args, err2, out2)
		}
	}
	// The kernel default query intervals are far longer than a scenario runs
	// (startup 31.25s, periodic 125s). With snooping on but no general query
	// inside the test window, the multicast mdb is populated only by each
	// listener's single unsolicited MLD join report at container start; if that
	// report races the veth/snooping attach and is missed, the bridge drops ALL
	// multicast to that listener for the whole run (intermittent received=0),
	// with no follow-up query to recover. Tighten the query cadence so the
	// bridge continuously re-solicits membership reports — any listener joining
	// at any point in any scenario is refreshed within the settle window,
	// making mdb population deterministic across the whole run.
	//
	// These knobs are not exposed via `ip link set type bridge`; write sysfs
	// directly, falling back to sudo tee if the process lacks the privilege.
	// (Note: there is no `mcast_querier6` sysfs file on modern kernels — the
	// single `multicast_querier` toggle set above covers both IGMP and MLD.)
	// Values are in centiseconds.
	sysfsSets := map[string]string{
		"multicast_query_interval":          "200", // periodic general query every 2s
		"multicast_query_response_interval": "100", // listeners report within 1s
		"multicast_startup_query_interval":  "100", // first queries 1s apart
		"multicast_startup_query_count":     "2",
	}
	for name, val := range sysfsSets {
		path := fmt.Sprintf("/sys/class/net/%s/bridge/%s", BridgeName, name)
		if err2 := writeSysfs(ctx, path, val); err2 != nil {
			fmt.Fprintf(os.Stderr, "[bridge] WARN %s: %v (non-fatal)\n", name, err2)
		}
	}

	// 4. Settle delay — only on first creation.
	if !already {
		fmt.Fprintf(os.Stderr, "[bridge] created %s; waiting 3s for MLD querier...\n", NetworkName)
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// DestroyMcastBridge removes the mcast-fabric Docker network.
// Idempotent — ignores "not found" errors.
func DestroyMcastBridge(ctx context.Context) error {
	out, err := run(ctx, "docker", "network", "rm", NetworkName)
	if err != nil && !strings.Contains(out, "not found") && !strings.Contains(out, "No such network") {
		return fmt.Errorf("docker network rm: %w\n%s", err, out)
	}
	return nil
}

// waitForIface polls until /sys/class/net/<name> exists or timeout expires.
func waitForIface(ctx context.Context, name string, timeout time.Duration) error {
	path := fmt.Sprintf("/sys/class/net/%s", name)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for %s", path)
}

// writeSysfs writes value to a sysfs path. If the direct write is denied it
// retries via "sudo tee" so the harness works when run without CAP_NET_ADMIN.
func writeSysfs(ctx context.Context, path, value string) error {
	if err := os.WriteFile(path, []byte(value), 0644); err == nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sudo", "tee", path)
	cmd.Stdin = strings.NewReader(value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo tee %s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// run executes a command and returns combined output + error.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
