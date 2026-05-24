// Package env wires together a Driver, a set of NodeConfigs, and helpers into
// a single Env value that test scenarios operate against.
package env

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/driver"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Env holds the runtime context for a single scenario execution.
type Env struct {
	t      *testing.T
	Driver driver.Driver
	nodes  []driver.NodeConfig
}

// New creates an Env bound to t with the given Driver.
func New(t *testing.T, d driver.Driver) *Env {
	t.Helper()
	return &Env{t: t, Driver: d}
}

// AddNode registers a node config to be started by StartAll.
func (e *Env) AddNode(cfg driver.NodeConfig) {
	e.nodes = append(e.nodes, cfg)
}

// PatchEnv merges extra environment variables into the named node's config.
// Must be called before StartAll.
func (e *Env) PatchEnv(name string, extra map[string]string) {
	for i, cfg := range e.nodes {
		if cfg.Name == name {
			if e.nodes[i].Env == nil {
				e.nodes[i].Env = make(map[string]string)
			}
			for k, v := range extra {
				e.nodes[i].Env[k] = v
			}
			return
		}
	}
	e.t.Fatalf("[env] PatchEnv: node %q not found", name)
}

// StartAll starts all registered nodes in registration order.
// A cleanup function is registered with t.Cleanup to stop all nodes on test exit.
func (e *Env) StartAll(ctx context.Context) {
	e.t.Helper()
	started := make([]string, 0, len(e.nodes))
	for _, cfg := range e.nodes {
		e.t.Logf("[env] starting %s (%s) at %s", cfg.Name, cfg.Image, cfg.IPv6)
		if err := e.Driver.Start(ctx, cfg); err != nil {
			// Stop already-started containers before failing.
			for _, n := range started {
				e.Driver.Stop(ctx, n) //nolint:errcheck
			}
			e.t.Fatalf("[env] start %s: %v", cfg.Name, err)
		}
		started = append(started, cfg.Name)
	}
	e.t.Cleanup(func() {
		cCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i := len(started) - 1; i >= 0; i-- {
			e.t.Logf("[env] stopping %s", started[i])
			if err := e.Driver.Stop(cCtx, started[i]); err != nil {
				e.t.Logf("[env] stop %s: %v", started[i], err)
			}
		}
	})
}

// MetricsURL returns the /metrics URL for the named node.
func (e *Env) MetricsURL(ctx context.Context, name string) string {
	e.t.Helper()
	url, err := e.Driver.MetricsURL(ctx, name)
	if err != nil {
		e.t.Fatalf("[env] MetricsURL %s: %v", name, err)
	}
	return url
}

// Snapshot scrapes all metrics from name and returns them.
func (e *Env) Snapshot(ctx context.Context, name string) map[string]float64 {
	e.t.Helper()
	url := e.MetricsURL(ctx, name)
	return metrics.Snapshot(e.t, name, url)
}

// WaitForExit waits up to timeout for name to exit.
func (e *Env) WaitForExit(ctx context.Context, name string, timeout time.Duration) int {
	e.t.Helper()
	tCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	code, err := e.Driver.WaitExit(tCtx, name)
	if err != nil {
		e.t.Fatalf("[env] WaitForExit %s: %v", name, err)
	}
	return code
}

// Sleep pauses execution for d, logging the reason.
func (e *Env) Sleep(d time.Duration, reason string) {
	e.t.Logf("[env] sleep %s (%s)", d, reason)
	time.Sleep(d)
}

// LogContainerOutput logs the last 50 lines of stdout/stderr from name.
func (e *Env) LogContainerOutput(ctx context.Context, name string) {
	e.t.Helper()
	out, err := dockerLogs(ctx, name)
	if err != nil {
		e.t.Logf("[env] logs %s: %v", name, err)
		return
	}
	e.t.Logf("[env] logs %s:\n%s", name, out)
}

// dockerLogs returns the last 50 lines of docker logs for name.
func dockerLogs(ctx context.Context, name string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", "50", name)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
