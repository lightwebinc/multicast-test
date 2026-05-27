// Package scenarios contains the Go-based test harness scenarios.
// Run with: sudo go test ./harness/scenarios/... -v -timeout 10m
//
// TestMain builds harness Docker images and sets up the multicast bridge
// once for all scenarios in the package.
package scenarios

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/build"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
)

// repoRoot is derived from the location of this file at runtime.
// All component repos are expected to be siblings under this root.
var repoRoot string

func TestMain(m *testing.M) {
	// Resolve repo root: two directories up from harness/scenarios/.
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(1)
	}
	// wd = /path/to/bitcoin-multicast-test/harness/scenarios
	// repoRoot = /path/to (parent of bitcoin-multicast-test)
	repoRoot = filepath.Dir(filepath.Dir(filepath.Dir(wd)))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// 1. Build harness Docker images from host-compiled binaries.
	specs := build.DefaultSpecs(repoRoot)
	if err := build.BuildAll(ctx, specs, false); err != nil {
		fmt.Fprintf(os.Stderr, "image build failed: %v\n", err)
		os.Exit(1)
	}

	// 2. Pull external images used by some scenarios (Redis, FRR).
	for _, img := range []string{"redis:7-alpine"} {
		if err := dockerPull(ctx, img); err != nil {
			fmt.Fprintf(os.Stderr, "pull %s failed: %v\n", img, err)
			os.Exit(1)
		}
	}

	// 3. Create the multicast bridge (idempotent).
	if err := dockerdriver.CreateMcastBridge(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "bridge setup failed: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	fmt.Fprintf(os.Stderr, "\n=== Scenario harness complete (exit code %d) ===\n", code)
	os.Exit(code)
}

// dockerPull pulls a Docker image if not already present locally.
func dockerPull(ctx context.Context, image string) error {
	// Check if already present.
	check := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image)
	if out, err := check.CombinedOutput(); err == nil && len(out) > 0 {
		fmt.Fprintf(os.Stderr, "[pull] %s: already present\n", image)
		return nil
	}
	fmt.Fprintf(os.Stderr, "[pull] %s: pulling...\n", image)
	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
