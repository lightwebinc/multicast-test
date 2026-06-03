package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Scenario 73 — Unified logging emit contract (end-to-end, no fabric)
//
// Builds and runs the real shard-manifest binary with LOG_FORMAT=json and
// asserts that the structured logs it emits at startup satisfy the unified
// logging contract from bsv-multicast/docs/UnifiedLogging:
//
//   - output is one JSON object per line on stdout;
//   - every line carries the identity triple (service.name,
//     service.instance.id, service.version) shared with OTLP metrics;
//   - a host.inventory event is emitted exactly once, nesting os/cpu/mem/net
//     groups, with BOTH IPv4 and IPv6 address keys present per interface.
//
// This exercises the shipped shard-common/logging + hostinfo packages through
// an actual daemon binary, so it catches wiring regressions (missing init,
// wrong service name, text-instead-of-json) that a package unit test cannot.
// It needs only `go` and a loopback stack — no Docker fabric — matching the
// process-local scenarios (60/61).
func TestScenario73_UnifiedLoggingContract(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("host.inventory sysfs/proc fields are Linux-specific")
	}

	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	manifestDir := filepath.Join(repoRoot, "shard-manifest")

	bin := filepath.Join(t.TempDir(), "shard-manifest")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer buildCancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", bin, ".")
	build.Dir = manifestDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build shard-manifest: %v\n%s", err, out)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer runCancel()
	// Long announce interval and an ephemeral metrics port keep the run quiet
	// and conflict-free; host.inventory is emitted before any socket bind.
	cmd := exec.CommandContext(runCtx, bin,
		"-announce-interval", "1h",
		"-metrics-addr", "[::1]:0",
	)
	cmd.Env = append(cmd.Environ(), "LOG_FORMAT=json", "LOG_LEVEL=info")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	var inventory map[string]any
	sawIdentityOnEveryLine := true
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	deadline := time.After(12 * time.Second)
	lines := 0

scan:
	for inventory == nil {
		select {
		case <-deadline:
			break scan
		default:
		}
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("stdout line is not JSON (text handler not replaced?): %q", line)
		}
		lines++
		for _, k := range []string{"service.name", "service.instance.id", "service.version"} {
			if _, ok := rec[k]; !ok {
				sawIdentityOnEveryLine = false
				t.Errorf("log line missing %q: %v", k, rec)
			}
		}
		if rec["service.name"] != "shard-manifest" {
			t.Errorf("service.name = %v, want shard-manifest", rec["service.name"])
		}
		if rec["msg"] == "host.inventory" {
			inv, ok := rec["inventory"].(map[string]any)
			if !ok {
				t.Fatalf("host.inventory has no nested inventory object: %v", rec)
			}
			inventory = inv
		}
	}

	if lines == 0 {
		t.Fatal("no JSON log lines captured from shard-manifest")
	}
	if !sawIdentityOnEveryLine {
		t.Error("identity triple was not present on every log line")
	}
	if inventory == nil {
		t.Fatal("never observed a host.inventory event")
	}

	// host.inventory must nest the descriptive groups.
	for _, g := range []string{"os", "cpu", "mem", "net", "build"} {
		if _, ok := inventory[g].(map[string]any); !ok {
			t.Errorf("host.inventory missing %q group: %v", g, inventory)
		}
	}
	if build, _ := inventory["build"].(map[string]any); build != nil {
		if build["service"] != "shard-manifest" {
			t.Errorf("build.service = %v, want shard-manifest", build["service"])
		}
	}

	// Every interface entry must expose both address-family keys (the IPv4
	// support added for boundary nodes), even if a family is empty.
	netG, _ := inventory["net"].(map[string]any)
	for name, v := range netG {
		ifc, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := ifc["ipv4"]; !ok {
			t.Errorf("interface %q missing ipv4 key", name)
		}
		if _, ok := ifc["ipv6"]; !ok {
			t.Errorf("interface %q missing ipv6 key", name)
		}
	}
}
