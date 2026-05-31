package scenarios

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/lightwebinc/shard-common/frame"
	"github.com/lightwebinc/shard-common/manifest"
)

// Scenario 70 — BRC-137 wire-level manifest pipeline
//
// Process-local sanity check that the BRC-137 sender → receiver →
// registry → evaluator pipeline works end-to-end on loopback. Does not
// require the Docker fabric; serves as the lower-cost smoke test before
// the full posture-aware E2E scenarios (which require PIM-SSM in the
// inter-container fabric and are not run by the default harness).
//
// What the scenario validates:
//   - Encode/decode round-trip across a real UDP socket
//   - MsgType demux (the receive loop accepts only 0x40)
//   - Registry keyed on (SrcIPv6, InstanceID)
//   - Evaluator quorum gate (2 distinct authoritative sources)
//   - PilotGroups projection from PilotOnly manifests
//
// Posture C (SSM) end-to-end across containers is exercised by the
// out-of-tree fabric tests; the same wire format runs unchanged here.
func TestScenario70_BRC137WirePipeline(t *testing.T) {
	t.Parallel()
	addr, err := net.ResolveUDPAddr("udp6", "[::1]:0")
	if err != nil {
		t.Skipf("udp6 loopback unavailable: %v", err)
	}
	recv, err := net.ListenUDP("udp6", addr)
	if err != nil {
		t.Skipf("ListenUDP: %v", err)
	}
	defer recv.Close()
	send, err := net.DialUDP("udp6", nil, recv.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Skipf("DialUDP: %v", err)
	}
	defer send.Close()

	reg := manifest.NewRegistry(60 * time.Second)
	ev := manifest.NewEvaluator(manifest.EvaluatorConfig{
		Quorum:     2,
		Hysteresis: 1 * time.Nanosecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_ = recv.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, src, err := recv.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			if n < 7 || buf[6] != frame.MsgTypeShardManifest {
				continue
			}
			m, err := frame.DecodeShardManifest(buf[:n])
			if err != nil {
				continue
			}
			srcAddr, _ := netip.AddrFromSlice(src.IP.To16())
			reg.Upsert(srcAddr, m)
		}
	}()

	mk := func(id uint32) *frame.ShardManifest {
		return &frame.ShardManifest{
			Flags: frame.ShardManifestFlagAuthoritative |
				frame.ShardManifestFlagGroupsValid |
				frame.ShardManifestFlagPilotOnly,
			InstanceID:       id,
			Epoch:            uint32(time.Now().Unix()),
			AnnounceInterval: 300,
			ShardBits:        8,
			RoleHint:         frame.RoleHintManifestOnly,
			Groups:           []uint16{0, 1, 2, 3},
		}
	}
	for _, id := range []uint32{0xC0FE0001, 0xC0FE0002} {
		m := mk(id)
		buf := make([]byte, frame.ShardManifestSize(m))
		n, err := frame.EncodeShardManifest(m, buf)
		if err != nil {
			t.Fatalf("Encode(%d): %v", id, err)
		}
		if _, err := send.Write(buf[:n]); err != nil {
			t.Fatalf("Write(%d): %v", id, err)
		}
	}

	var adopted manifest.Adopted
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reg.Len() >= 2 {
			_ = ev.Evaluate(reg.Snapshot())
			time.Sleep(2 * time.Millisecond)
			adopted = ev.Evaluate(reg.Snapshot())
			if adopted.QuorumMet["shard_bits"] {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !adopted.QuorumMet["shard_bits"] {
		t.Fatalf("quorum never met; adopted=%+v", adopted)
	}
	if adopted.ShardBits != 8 {
		t.Errorf("ShardBits = %d, want 8", adopted.ShardBits)
	}
	if len(adopted.PilotGroups) != 4 {
		t.Errorf("PilotGroups len = %d, want 4", len(adopted.PilotGroups))
	}
	cancel()
	wg.Wait()
}

// Scenario 71 — BRC-137 Successor block live-reshard signal
//
// Same pipeline as Scenario 70 but with a Successor block on each
// authoritative manifest. Verifies the evaluator surfaces a
// SuccessorView once quorum is met. Posture-agnostic; the bridging
// behavior (proxy dual-emit, listener union-join) is exercised in the
// per-component unit tests (shard-proxy/forwarder bridging tests, etc.)
// and would be confirmed end-to-end on a posture-C fabric.
func TestScenario71_BRC137SuccessorSignal(t *testing.T) {
	t.Parallel()
	addr, err := net.ResolveUDPAddr("udp6", "[::1]:0")
	if err != nil {
		t.Skipf("udp6 loopback unavailable: %v", err)
	}
	recv, err := net.ListenUDP("udp6", addr)
	if err != nil {
		t.Skipf("ListenUDP: %v", err)
	}
	defer recv.Close()
	send, err := net.DialUDP("udp6", nil, recv.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Skipf("DialUDP: %v", err)
	}
	defer send.Close()

	reg := manifest.NewRegistry(60 * time.Second)
	ev := manifest.NewEvaluator(manifest.EvaluatorConfig{
		Quorum:     2,
		Hysteresis: 1 * time.Nanosecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_ = recv.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			n, src, err := recv.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}
			if n < 7 || buf[6] != frame.MsgTypeShardManifest {
				continue
			}
			m, err := frame.DecodeShardManifest(buf[:n])
			if err != nil {
				continue
			}
			srcAddr, _ := netip.AddrFromSlice(src.IP.To16())
			reg.Upsert(srcAddr, m)
		}
	}()

	successorEpoch := uint32(time.Now().Add(1 * time.Hour).Unix())
	mk := func(id uint32) *frame.ShardManifest {
		m := &frame.ShardManifest{
			Flags: frame.ShardManifestFlagAuthoritative |
				frame.ShardManifestFlagGroupsValid |
				frame.ShardManifestFlagPilotOnly |
				frame.ShardManifestFlagSuccessorValid,
			InstanceID:       id,
			Epoch:            uint32(time.Now().Unix()),
			AnnounceInterval: 300,
			ShardBits:        8,
			Groups:           []uint16{0, 1, 2, 3},
			Successor: &frame.SuccessorBlock{
				ShardBits:       9,
				Flags:           frame.SuccessorFlagSourceModeSSM,
				TransitionEpoch: successorEpoch,
			},
		}
		copy(m.Successor.GenerationID[:], []byte("scenario71-gen!"))
		return m
	}
	for _, id := range []uint32{0xD1F00001, 0xD1F00002} {
		m := mk(id)
		buf := make([]byte, frame.ShardManifestSize(m))
		n, err := frame.EncodeShardManifest(m, buf)
		if err != nil {
			t.Fatalf("Encode(%d): %v", id, err)
		}
		if _, err := send.Write(buf[:n]); err != nil {
			t.Fatalf("Write(%d): %v", id, err)
		}
	}

	var adopted manifest.Adopted
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reg.Len() >= 2 {
			_ = ev.Evaluate(reg.Snapshot())
			time.Sleep(2 * time.Millisecond)
			adopted = ev.Evaluate(reg.Snapshot())
			if adopted.Successor != nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if adopted.Successor == nil {
		t.Fatalf("Successor never adopted; adopted=%+v", adopted)
	}
	if adopted.Successor.ShardBits != 9 {
		t.Errorf("Successor.ShardBits = %d, want 9", adopted.Successor.ShardBits)
	}
	if !adopted.Successor.SourceModeSSM {
		t.Errorf("Successor.SourceModeSSM = false, want true")
	}
	if adopted.Successor.TransitionEpoch != successorEpoch {
		t.Errorf("Successor.TransitionEpoch = %d, want %d", adopted.Successor.TransitionEpoch, successorEpoch)
	}
	cancel()
	wg.Wait()
}
