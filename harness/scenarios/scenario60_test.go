package scenarios

import (
	"net"
	"net/netip"
	"runtime"
	"testing"

	"github.com/lightwebinc/shard-common/netjoin"
	"github.com/lightwebinc/shard-common/shard"
)

// Scenario 60 — SSM loopback Join/Leave
//
// Process-local sanity check that shard-common/netjoin's branched
// ASM/SSM join/leave path works on this kernel. Does not require the
// Docker fabric — it operates on lo, so it also serves as an
// early-warning if the test host lacks SSM kernel support (e.g.
// MCAST_JOIN_SOURCE_GROUP rejected, mld_max_msf at default 64).
//
// Posture C end-to-end across containers requires PIM-SSM in the
// inter-container fabric, which Docker's default bridge does not
// provide; that is validated on real fabric hosts (not in this
// harness). See bsv-multicast SSM Support Plan for the design.
func TestScenario60_SSMLoopback(t *testing.T) {
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
