// Package env wires together a Driver, a set of NodeConfigs, and helpers into
// a single Env value that test scenarios operate against.
package env

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// readyTimeout is the upper bound applied by StartAll to each service node's
// /readyz probe. Generous enough to tolerate cold-Docker IPv6 DAD + image
// layer pulls; small enough to surface stuck containers quickly.
const readyTimeout = 30 * time.Second

// serviceRoles enumerates the roles whose containers expose a /readyz
// endpoint that StartAll should block on before declaring the node up.
var serviceRoles = map[driver.Role]bool{
	driver.RoleProxy:    true,
	driver.RoleListener: true,
	driver.RoleRetry:    true,
}

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

// StartAll starts all registered nodes in registration order, then blocks
// until each service node (proxy/listener/retry) reports ready via /readyz.
// A cleanup function is registered with t.Cleanup to stop all nodes on test
// exit.
func (e *Env) StartAll(ctx context.Context) {
	e.t.Helper()
	started := make([]string, 0, len(e.nodes))
	stopStarted := func() {
		for _, n := range started {
			e.Driver.Stop(ctx, n) //nolint:errcheck
		}
	}
	for _, cfg := range e.nodes {
		e.t.Logf("[env] starting %s (%s) at %s", cfg.Name, cfg.Image, cfg.IPv6)
		if err := e.Driver.Start(ctx, cfg); err != nil {
			// Remove the failing node's husk (docker may leave a Created
			// record on network setup failure) plus any already-started
			// containers before failing.
			e.Driver.Stop(ctx, cfg.Name) //nolint:errcheck
			stopStarted()
			e.t.Fatalf("[env] start %s: %v", cfg.Name, err)
		}
		started = append(started, cfg.Name)
		// Block until the node's /readyz returns 200 so subsequent steps
		// (snapshotting metrics, starting the source) never race against
		// proxy boot, IPv6 DAD, or worker socket-bind. Skipped for
		// non-service roles (generator/aux/BGP) which have no /readyz.
		if cfg.MetricsPort > 0 && serviceRoles[cfg.Role] {
			if err := e.waitReady(ctx, cfg.Name, readyTimeout); err != nil {
				e.LogContainerOutput(ctx, cfg.Name)
				stopStarted()
				e.t.Fatalf("[env] %s not ready: %v", cfg.Name, err)
			}
		}
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

// WaitReady polls the named node's /readyz endpoint until it returns 200 OK
// or timeout elapses. Service nodes (proxy/listener/retry) all expose
// /readyz on the same port as /metrics; the handler reports 200 only after
// the underlying workers have bound their sockets and entered their receive
// loops, so a successful probe is a strong "fully accepting traffic"
// signal. Exported for scenarios that need to wait for late-started nodes
// outside the StartAll flow.
func (e *Env) WaitReady(ctx context.Context, name string, timeout time.Duration) error {
	e.t.Helper()
	return e.waitReady(ctx, name, timeout)
}

func (e *Env) waitReady(ctx context.Context, name string, timeout time.Duration) error {
	pCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	metricsURL, err := e.Driver.MetricsURL(pCtx, name)
	if err != nil {
		return fmt.Errorf("resolve metrics URL: %w", err)
	}
	// /metrics → /readyz on the same host:port.
	readyzURL := strings.Replace(metricsURL, "/metrics", "/readyz", 1)

	client := &http.Client{Timeout: 1 * time.Second}
	backoff := 50 * time.Millisecond
	var lastErr error
	var lastStatus int
	for {
		req, _ := http.NewRequestWithContext(pCtx, http.MethodGet, readyzURL, nil)
		resp, err := client.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				e.t.Logf("[env] %s ready (readyz 200 at %s)", name, readyzURL)
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-pCtx.Done():
			return fmt.Errorf("timeout after %s polling %s (last err: %v, last status: %d)",
				timeout, readyzURL, lastErr, lastStatus)
		case <-time.After(backoff):
		}
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}
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
