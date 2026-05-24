package env

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// containerPID returns the host PID 1 of the named Docker container.
func containerPID(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.State.Pid}}", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker inspect pid %s: %w", name, err)
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" || pid == "0" {
		return "", fmt.Errorf("container %s not running (pid=%s)", name, pid)
	}
	return pid, nil
}

// nsenterRun executes cmd inside the network namespace of the named container
// using nsenter. This avoids needing tc/ip6tables binaries inside distroless
// images — we use the host's copies via the container's network namespace.
func nsenterRun(ctx context.Context, containerName string, args ...string) error {
	pid, err := containerPID(ctx, containerName)
	if err != nil {
		return err
	}
	nsArgs := append([]string{"--net=/proc/" + pid + "/ns/net"}, args...)
	cmd := exec.CommandContext(ctx, "nsenter", nsArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nsenter %v: %w\n%s", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// hostVethFor returns the name of the host-side veth interface that is the
// peer of eth0 inside the named container. Traffic flowing OUT of this
// interface is traffic flowing IN to the container's eth0, so a tc qdisc
// applied here simulates ingress packet loss for the container.
func hostVethFor(ctx context.Context, containerName string) (string, error) {
	pid, err := containerPID(ctx, containerName)
	if err != nil {
		return "", err
	}
	// Get eth0 link info from inside the container namespace.
	// Output looks like: "2: eth0@if1931: <FLAGS> ..."
	// The "@if<N>" suffix is the peer's ifindex on the host.
	cmd := exec.CommandContext(ctx, "nsenter", "--net=/proc/"+pid+"/ns/net",
		"ip", "link", "show", "eth0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip link show eth0 in %s: %w: %s", containerName, err, out)
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	atIdx := strings.Index(line, "@if")
	if atIdx < 0 {
		return "", fmt.Errorf("no @if found in ip link output: %s", line)
	}
	peerStr := strings.TrimRight(strings.Fields(line[atIdx+3:])[0], ":")
	peerIdx, err := strconv.Atoi(peerStr)
	if err != nil {
		return "", fmt.Errorf("parse peer ifindex from %q: %w", peerStr, err)
	}
	// Find the host interface whose ifindex matches the peer.
	cmd2 := exec.CommandContext(ctx, "ip", "link", "show")
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip link show on host: %w: %s", err, out2)
	}
	prefix := fmt.Sprintf("%d: ", peerIdx)
	for _, l := range strings.Split(string(out2), "\n") {
		if strings.HasPrefix(l, prefix) {
			name := strings.TrimPrefix(l, prefix)
			if at := strings.Index(name, "@"); at >= 0 {
				name = name[:at]
			} else if col := strings.Index(name, ":"); col >= 0 {
				name = name[:col]
			}
			return strings.TrimSpace(name), nil
		}
	}
	return "", fmt.Errorf("no host interface with ifindex %d", peerIdx)
}

// ApplyNetemLoss adds a tc netem qdisc to the HOST-SIDE veth peer of the
// container's eth0. Because outgoing traffic from the host veth is incoming
// traffic for the container, this simulates ingress packet loss at the
// container (e.g. dropped multicast datagrams before the listener processes them).
func ApplyNetemLoss(ctx context.Context, containerName string, lossPct float64) error {
	veth, err := hostVethFor(ctx, containerName)
	if err != nil {
		return fmt.Errorf("hostVethFor %s: %w", containerName, err)
	}
	cmd := exec.CommandContext(ctx, "tc", "qdisc", "add", "dev", veth,
		"root", "netem", "loss", fmt.Sprintf("%.1f%%", lossPct))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tc qdisc add on %s: %w\n%s", veth, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveNetemLoss removes the tc netem qdisc from the host-side veth peer of
// the container's eth0.
func RemoveNetemLoss(ctx context.Context, containerName string) error {
	veth, err := hostVethFor(ctx, containerName)
	if err != nil {
		return fmt.Errorf("hostVethFor %s: %w", containerName, err)
	}
	cmd := exec.CommandContext(ctx, "tc", "qdisc", "del", "dev", veth, "root")
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such") {
		return fmt.Errorf("tc qdisc del on %s: %w\n%s", veth, err, strings.TrimSpace(string(out)))
	}
	return nil
}
