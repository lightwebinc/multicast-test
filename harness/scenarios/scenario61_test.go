package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
)

// Scenario 61 — SSM ASM-fallback startup
//
// Validates that a proxy + listener configured with sourceMode=asm
// still pass startup health checks when the SSM scaffolding is
// present in the image. The new SOURCE_MODE env var must be accepted
// without changing ASM behavior — this is the non-skipping
// counterpart to Scenario 60 (loopback Join/Leave) and the
// regression guard against accidentally making the SSM code path
// non-optional.
//
// Full Posture C cross-container SSM data delivery requires PIM-SSM
// in the fabric (not provided by Docker's default bridge) and is
// covered on real fabric hosts.
func TestScenario61_SSMASMFallback(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:  "s61-proxy",
		Image: "shard-proxy:harness",
		IPv6:  "fd10::2",
		Env: map[string]string{
			"MULTICAST_IF":    "eth0",
			"UDP_LISTEN_PORT": "8725",
			"EGRESS_PORT":     "9001",
			"SHARD_BITS":      "2",
			"MC_SCOPE":        "site",
			"MC_GROUP_ID":     "0x000B",
			"METRICS_ADDR":    ":9100",
			// SSM scaffolding present but mode=asm — must not affect behavior.
			"SOURCE_MODE": "asm",
		},
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	e.AddNode(driver.NodeConfig{
		Name:  "s61-listener",
		Image: "shard-listener:harness",
		IPv6:  "fd10::11",
		Env: map[string]string{
			"MULTICAST_IF": "eth0",
			"LISTEN_PORT":  "9001",
			"SHARD_BITS":   "2",
			"MC_SCOPE":     "site",
			"MC_GROUP_ID":  "0x000B",
			"NUM_WORKERS":  "1",
			"EGRESS_ADDR":  "127.0.0.1:9100",
			"METRICS_ADDR": ":9200",
			"SOURCE_MODE":  "asm",
		},
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(2*time.Second, "MLDv2 settle")

	// If either container exited (config-rejected on SOURCE_MODE), e.StartAll
	// would have already failed. Health checks past this point validate the
	// new env vars are accepted in ASM mode.
	urlProxy := e.MetricsURL(ctx, "s61-proxy")
	urlListener := e.MetricsURL(ctx, "s61-listener")
	if urlProxy == "" || urlListener == "" {
		t.Fatal("metrics URL missing — container failed to start")
	}
}
