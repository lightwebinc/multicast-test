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

// Scenario 72 — BRC-137 adoption safety gates
//
// Companion to Scenario 70 (happy-path quorum adoption). Where 70 proves
// the sender → socket → registry → evaluator pipeline ADOPTS when quorum
// agrees, this scenario proves it REFUSES to adopt from insufficient or
// conflicting signal — the load-bearing safety property of
// auto-shard-config. A bug here means a single rogue or misconfigured
// announcer could reshard the fleet.
//
// Three negative paths are exercised over the same loopback wire pipeline:
//   - sub-quorum: one authoritative announcer, Quorum=2 ⇒ no adoption
//   - non-authoritative: two announcers without the Authoritative flag ⇒
//     PilotsKnown=0, no adoption
//   - divergence: two authoritative announcers proposing different
//     ShardBits ⇒ neither value reaches quorum, divergence is flagged
//
// Posture-agnostic; the same wire format runs unchanged on a posture-C
// fabric.
func TestScenario72_BRC137AdoptionSafetyGates(t *testing.T) {
	t.Parallel()

	// runPipeline ships manifests over a real loopback UDP socket into a
	// Registry, then evaluates with the given quorum. It waits until all
	// distinct (src,InstanceID) entries land before evaluating. Returns the
	// adopted view. Evaluate is called twice (hysteresis 1ns) so an eligible
	// candidate would clear the hysteresis floor — mirroring Scenario 70.
	runPipeline := func(t *testing.T, quorum int, manifests []*frame.ShardManifest) manifest.Adopted {
		t.Helper()
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
			Quorum:     quorum,
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
				m, derr := frame.DecodeShardManifest(buf[:n])
				if derr != nil {
					continue
				}
				srcAddr, _ := netip.AddrFromSlice(src.IP.To16())
				reg.Upsert(srcAddr, m)
			}
		}()

		for _, m := range manifests {
			buf := make([]byte, frame.ShardManifestSize(m))
			n, err := frame.EncodeShardManifest(m, buf)
			if err != nil {
				t.Fatalf("Encode(%d): %v", m.InstanceID, err)
			}
			if _, err := send.Write(buf[:n]); err != nil {
				t.Fatalf("Write(%d): %v", m.InstanceID, err)
			}
		}

		// Wait for all entries to land in the registry.
		want := len(manifests)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && reg.Len() < want {
			time.Sleep(5 * time.Millisecond)
		}
		if reg.Len() < want {
			t.Fatalf("only %d/%d manifests reached the registry", reg.Len(), want)
		}

		_ = ev.Evaluate(reg.Snapshot())
		time.Sleep(2 * time.Millisecond)
		adopted := ev.Evaluate(reg.Snapshot())

		cancel()
		wg.Wait()
		return adopted
	}

	authoritative := func(id uint32, shardBits uint8) *frame.ShardManifest {
		return &frame.ShardManifest{
			Flags: frame.ShardManifestFlagAuthoritative |
				frame.ShardManifestFlagGroupsValid |
				frame.ShardManifestFlagPilotOnly,
			InstanceID:       id,
			Epoch:            uint32(time.Now().Unix()),
			AnnounceInterval: 300,
			ShardBits:        shardBits,
			RoleHint:         frame.RoleHintManifestOnly,
			Groups:           []uint16{0, 1, 2, 3},
		}
	}

	t.Run("sub-quorum", func(t *testing.T) {
		t.Parallel()
		// One authoritative announcer, quorum requires two.
		adopted := runPipeline(t, 2, []*frame.ShardManifest{
			authoritative(0x5AFE0001, 8),
		})
		if adopted.PilotsKnown != 1 {
			t.Errorf("PilotsKnown = %d, want 1", adopted.PilotsKnown)
		}
		if adopted.QuorumMet["shard_bits"] {
			t.Error("quorum met with a single announcer; fleet would reshard on one signal")
		}
		if adopted.ShardBits != 0 {
			t.Errorf("ShardBits = %d, want 0 (nothing adopted)", adopted.ShardBits)
		}
	})

	t.Run("non-authoritative-ignored", func(t *testing.T) {
		t.Parallel()
		// Two announcers, but neither carries the Authoritative flag.
		mk := func(id uint32) *frame.ShardManifest {
			m := authoritative(id, 8)
			m.Flags &^= frame.ShardManifestFlagAuthoritative // strip authority
			return m
		}
		adopted := runPipeline(t, 2, []*frame.ShardManifest{
			mk(0x5AFE0011), mk(0x5AFE0012),
		})
		if adopted.PilotsKnown != 0 {
			t.Errorf("PilotsKnown = %d, want 0 (observational manifests are not pilots)", adopted.PilotsKnown)
		}
		if adopted.QuorumMet["shard_bits"] {
			t.Error("non-authoritative announcers must not satisfy quorum")
		}
		if len(adopted.PilotGroups) != 0 {
			t.Errorf("PilotGroups = %v, want none (no authoritative pilots)", adopted.PilotGroups)
		}
	})

	t.Run("divergence", func(t *testing.T) {
		t.Parallel()
		// Two authoritative announcers, but they disagree on ShardBits, so
		// no single value reaches quorum=2.
		adopted := runPipeline(t, 2, []*frame.ShardManifest{
			authoritative(0x5AFE0021, 8),
			authoritative(0x5AFE0022, 9),
		})
		if adopted.PilotsKnown != 2 {
			t.Errorf("PilotsKnown = %d, want 2", adopted.PilotsKnown)
		}
		if adopted.QuorumMet["shard_bits"] {
			t.Error("conflicting ShardBits proposals must not satisfy quorum")
		}
		found := false
		for _, f := range adopted.DivergenceFields {
			if f == "shard_bits" {
				found = true
			}
		}
		if !found {
			t.Errorf("DivergenceFields = %v, want it to include shard_bits", adopted.DivergenceFields)
		}
	})
}
