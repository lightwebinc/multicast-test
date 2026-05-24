package env

import (
	"context"
	"fmt"
	"os/exec"
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

// ApplyNetemLoss adds a tc netem qdisc with the given loss percentage to
// the container's eth0 interface (via nsenter into the container's netns).
func ApplyNetemLoss(ctx context.Context, containerName string, lossPct float64) error {
	return nsenterRun(ctx, containerName,
		"tc", "qdisc", "add", "dev", "eth0", "root", "netem",
		"loss", fmt.Sprintf("%.1f%%", lossPct))
}

// RemoveNetemLoss removes the tc netem qdisc from the container's eth0.
func RemoveNetemLoss(ctx context.Context, containerName string) error {
	return nsenterRun(ctx, containerName,
		"tc", "qdisc", "del", "dev", "eth0", "root")
}
