// Package driver defines the Driver interface and supporting types used by the
// Go test harness to start, inspect, and stop test nodes regardless of the
// underlying runtime (Docker, LXD, etc.).
package driver

import "context"

// Role identifies the functional role of a node in the test topology.
type Role string

const (
	RoleProxy     Role = "proxy"
	RoleListener  Role = "listener"
	RoleRetry     Role = "retry"
	RoleGenerator Role = "generator"
)

// NodeConfig describes how a single node should be provisioned.
type NodeConfig struct {
	// Name is the unique container / VM name within this test run.
	Name string
	// Image is the OCI image tag (Docker driver) or profile name (LXD driver).
	Image string
	// IPv6 is the static IPv6 address to assign on the test fabric network.
	IPv6 string
	// Env is the set of environment variables to pass to the process.
	Env map[string]string
	// Cmd overrides the container ENTRYPOINT command (nil = use image default).
	Cmd []string
	// MetricsPort is the TCP port where /metrics is served (0 = no metrics).
	MetricsPort int
	// Role is informational; used for topology queries.
	Role Role
}

// Driver is the interface that wraps the lifecycle of test nodes.
// Implementations must be safe for concurrent use.
type Driver interface {
	// Start provisions and starts the named node according to cfg.
	Start(ctx context.Context, cfg NodeConfig) error
	// Stop stops and removes the named node. Idempotent.
	Stop(ctx context.Context, name string) error
	// Exec runs a command inside the named node and returns combined output.
	Exec(ctx context.Context, name string, cmd string, args ...string) (string, error)
	// Addr returns the IPv6 address of the named node on the test fabric.
	Addr(ctx context.Context, name string) (string, error)
	// MetricsURL returns the full HTTP URL for the /metrics endpoint of name.
	MetricsURL(ctx context.Context, name string) (string, error)
	// WaitExit blocks until the named node's main process exits or ctx is done.
	// Returns the exit code; -1 on timeout or error.
	WaitExit(ctx context.Context, name string) (int, error)
}
