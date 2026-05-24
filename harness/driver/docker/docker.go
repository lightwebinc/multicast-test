package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/driver"
)

// Driver implements driver.Driver using the local Docker CLI.
type Driver struct{}

var _ driver.Driver = (*Driver)(nil)

// New returns a Docker CLI driver.
func New() *Driver { return &Driver{} }

// Start runs a detached container with the given config.
func (d *Driver) Start(ctx context.Context, cfg driver.NodeConfig) error {
	args := []string{
		"run", "-d",
		"--name", cfg.Name,
		"--network", NetworkName,
		"--ip6", cfg.IPv6,
		"--cap-add", "NET_ADMIN",
	}
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}
	// No -p host port mapping: metrics are scraped via the container's direct
	// IPv6 address on the mcast-fabric bridge, accessible from the test host.
	args = append(args, cfg.Image)
	// cfg.Cmd elements are appended after the image: they are passed as CMD
	// arguments to the container's ENTRYPOINT (do not include the binary name).
	args = append(args, cfg.Cmd...)

	out, err := run(ctx, "docker", args...)
	if err != nil {
		return fmt.Errorf("docker run %s: %w\n%s", cfg.Name, err, out)
	}
	return nil
}

// Stop stops and removes the named container. Idempotent.
func (d *Driver) Stop(ctx context.Context, name string) error {
	// Stop (ignore error — container may already be stopped or not exist).
	run(ctx, "docker", "stop", "--time", "5", name) //nolint:errcheck
	// Remove.
	out, err := run(ctx, "docker", "rm", "-f", name)
	if err != nil && !strings.Contains(out, "No such container") {
		return fmt.Errorf("docker rm %s: %w\n%s", name, err, out)
	}
	return nil
}

// Exec runs cmd inside the named container and returns combined stdout+stderr.
func (d *Driver) Exec(ctx context.Context, name string, cmd string, args ...string) (string, error) {
	execArgs := append([]string{"exec", name, cmd}, args...)
	return run(ctx, "docker", execArgs...)
}

// Addr returns the pre-configured IPv6 from the node's last Start call.
// For the Docker driver this is the static IPv6 from cfg.IPv6 assigned at start time.
// This implementation does a live inspect to confirm the container is running.
func (d *Driver) Addr(ctx context.Context, name string) (string, error) {
	out, err := run(ctx, "docker", "inspect",
		"--format", "{{range .NetworkSettings.Networks}}{{.GlobalIPv6Address}}{{end}}",
		name,
	)
	if err != nil {
		return "", fmt.Errorf("docker inspect %s: %w\n%s", name, err, out)
	}
	return strings.TrimSpace(out), nil
}

// MetricsURL returns the HTTP URL for the /metrics endpoint.
// Accesses via the container's direct IPv6 on the fabric network.
func (d *Driver) MetricsURL(ctx context.Context, name string) (string, error) {
	addr, err := d.Addr(ctx, name)
	if err != nil {
		return "", err
	}
	port, err := metricsPortFor(ctx, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://[%s]:%d/metrics", addr, port), nil
}

// WaitExit blocks until the container exits or ctx expires.
// Returns the container's exit code, or -1 on timeout/error.
func (d *Driver) WaitExit(ctx context.Context, name string) (int, error) {
	// Poll container status every 500ms.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-ticker.C:
			out, err := run(ctx, "docker", "inspect",
				"--format", "{{.State.Status}}",
				name,
			)
			if err != nil {
				return -1, fmt.Errorf("docker inspect %s: %w", name, err)
			}
			status := strings.TrimSpace(out)
			if status == "exited" || status == "dead" {
				code, _ := run(ctx, "docker", "inspect",
					"--format", "{{.State.ExitCode}}",
					name,
				)
				var exitCode int
				fmt.Sscanf(strings.TrimSpace(code), "%d", &exitCode)
				return exitCode, nil
			}
		}
	}
}

// metricsPortFor returns the METRICS_ADDR port configured for the container by
// reading the container's environment.
func metricsPortFor(ctx context.Context, name string) (int, error) {
	out, err := run(ctx, "docker", "inspect",
		"--format", `{{range .Config.Env}}{{.}}{{"\n"}}{{end}}`,
		name,
	)
	if err != nil {
		return 0, fmt.Errorf("docker inspect env %s: %w", name, err)
	}
	for _, line := range strings.Split(out, "\n") {
		var key, val string
		if _, err2 := fmt.Sscanf(line, "%s", &key); err2 != nil {
			continue
		}
		if !strings.HasPrefix(key, "METRICS_ADDR=") {
			continue
		}
		val = strings.TrimPrefix(key, "METRICS_ADDR=")
		val = strings.TrimPrefix(val, ":")
		var port int
		if _, err2 := fmt.Sscanf(val, "%d", &port); err2 == nil && port > 0 {
			return port, nil
		}
	}
	return 9200, nil // default listener metrics port
}
