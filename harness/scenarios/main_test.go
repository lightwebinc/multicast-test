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
	"path/filepath"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/build"
	dockerdriver "github.com/lightwebinc/bitcoin-multicast-test/harness/driver/docker"
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

	// 2. Create the multicast bridge (idempotent).
	if err := dockerdriver.CreateMcastBridge(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "bridge setup failed: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
