package scenarios

import (
	"context"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/lightwebinc/shard-common/netjoin"
	"github.com/lightwebinc/shard-common/shard"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
)

// TestSSM_Loopback_JoinLeave is a process-local sanity check that
// shard-common/netjoin's branched ASM/SSM join/leave path works on this
// kernel. It does not require the Docker fabric — it operates on lo, so it
// also serves as an early-warning if the test host lacks SSM kernel
// support.
//
// Posture C end-to-end across containers requires PIM-SSM in the
// inter-container fabric, which Docker's default bridge does not provide;
// that is left to the LXD VM lab (vm-lab/) or to a real fabric host.
func TestSSM_Loopback_JoinLeave(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SSM loopback test requires Linux")
	}
	t.Parallel()

	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatalf("lo: %v", err)
	}

	// Open a UDP6 socket on the kernel-selected port.
	pc, err := net.ListenPacket("udp6", "[::]:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer func() { _ = pc.Close() }()
	uc := pc.(*net.UDPConn)
	raw, err := uc.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}

	// Derive the SSM group address from (SSM, site).
	prefix, err := shard.Prefix(shard.SourceModeSSM, shard.ScopeSite)
	if err != nil {
		t.Fatalf("Prefix: %v", err)
	}
	if prefix != 0xFF35 {
		t.Errorf("SSM site prefix = %#x, want 0xFF35", prefix)
	}
	grpIP := shard.GroupAddr(prefix, shard.DefaultGroupID, shard.GroupIdx(0))
	grpAddr, _ := netip.AddrFromSlice(grpIP)
	srcs := []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("fd00::1")}

	var joinErr, leaveErr error
	cerr := raw.Control(func(fd uintptr) {
		joinErr = netjoin.Join(int(fd), lo.Index, grpAddr, srcs)
		if joinErr == nil {
			leaveErr = netjoin.Leave(int(fd), lo.Index, grpAddr, srcs)
		}
	})
	if cerr != nil {
		t.Fatalf("Control: %v", cerr)
	}
	if joinErr != nil {
		t.Fatalf("SSM Join (S,G) for %d sources: %v (kernel may lack MCAST_JOIN_SOURCE_GROUP; check mld_max_msf)", len(srcs), joinErr)
	}
	if leaveErr != nil {
		t.Fatalf("SSM Leave: %v", leaveErr)
	}
}

// TestSSM_Scenario_ASMFallback validates that a proxy + listener
// configured with sourceMode=asm still pass startup health checks (the
// SSM scaffolding must be no-op in ASM mode). This is the non-skipping
// counterpart to a full Posture C scenario — it exercises the new env
// vars without depending on Docker fabric SSM support.
func TestSSM_Scenario_ASMFallback(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:  "ssm-asm-proxy",
		Image: "shard-proxy:harness",
		IPv6:  "fd10::2",
		Env: map[string]string{
			"MULTICAST_IF":    "eth0",
			"UDP_LISTEN_PORT": "9000",
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
		Name:  "ssm-asm-listener",
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
	urlProxy := e.MetricsURL(ctx, "ssm-asm-proxy")
	urlListener := e.MetricsURL(ctx, "ssm-asm-listener")
	if urlProxy == "" || urlListener == "" {
		t.Fatal("metrics URL missing — container failed to start")
	}
}
