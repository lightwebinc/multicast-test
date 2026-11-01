package scenarios

import (
	"net/netip"
	"testing"
	"time"

	"github.com/lightwebinc/shard-common/frame"
	"github.com/lightwebinc/shard-common/manifest"
)

// Scenario 97: BRC-148 per-domain manifest coordination (harness only)
//
// The BRC-139 Domains extension end to end in-process: authoritative
// manifests carrying a BEEF-plane descriptor are wire-encoded, decoded,
// registered, and evaluated. Quorum + hysteresis adopt the plane's
// shard-bits independently of the top-level (domain-0) fields; divergent
// announcers block adoption and surface per-domain divergence; a plain
// BRC-139 manifest (DomainsValid=0) is untouched (backward compatibility).
func TestScenario97_BeefDomainAdoption(t *testing.T) {
	buildManifest := func(instance uint32, srcLow byte, beefBits uint8, domains bool) (*frame.ShardManifest, netip.Addr) {
		m := &frame.ShardManifest{
			Flags:            frame.ShardManifestFlagAuthoritative,
			InstanceID:       instance,
			TTL:              60,
			AnnounceInterval: 1,
			ShardBits:        8,
			RoleHint:         frame.RoleHintListenerBEEF,
		}
		var src [16]byte
		src[0], src[15] = 0xfd, srcLow
		copy(m.SrcIPv6[:], src[:])
		if domains {
			m.Flags |= frame.ShardManifestFlagDomainsValid
			m.Domains = []frame.DomainDescriptor{{
				DomainID:  0x1,
				ShardBits: beefBits,
				SlotSpan:  1,
				Flags:     frame.DomainFlagSourceModeSSM | frame.DomainFlagActive,
			}}
		}
		return m, netip.AddrFrom16(src)
	}

	// Wire pipeline: encode → decode → registry, exactly what the beacon
	// demux does with a received datagram.
	pipeline := func(t *testing.T, manifests []*frame.ShardManifest, srcs []netip.Addr) manifest.Adopted {
		t.Helper()
		reg := manifest.NewRegistry(0)
		for i, m := range manifests {
			buf := make([]byte, frame.ShardManifestSize(m))
			if _, err := frame.EncodeShardManifest(m, buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			decoded, err := frame.DecodeShardManifest(buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			reg.Upsert(srcs[i], decoded)
		}
		ev := manifest.NewEvaluator(manifest.EvaluatorConfig{Quorum: 2, Hysteresis: time.Nanosecond})
		ev.Evaluate(reg.Snapshot()) // arm hysteresis
		time.Sleep(2 * time.Millisecond)
		return ev.Evaluate(reg.Snapshot())
	}

	t.Run("quorum adopts the plane width", func(t *testing.T) {
		m1, s1 := buildManifest(1, 0x01, 12, true)
		m2, s2 := buildManifest(2, 0x02, 12, true)
		got := pipeline(t, []*frame.ShardManifest{m1, m2}, []netip.Addr{s1, s2})
		da := got.Domains[0x1]
		if da.ShardBits != 12 || !da.SourceModeSSM || !da.QuorumMet {
			t.Fatalf("beef plane not adopted: %+v", da)
		}
		// Domain-0 stays governed by the top-level fields.
		if got.ShardBits != 8 {
			t.Fatalf("top-level ShardBits = %d, want 8", got.ShardBits)
		}
		if _, ok := got.Domains[0x0]; ok {
			t.Fatal("domain 0 must not appear in the per-plane view")
		}
	})

	t.Run("divergent announcers block adoption", func(t *testing.T) {
		m1, s1 := buildManifest(1, 0x01, 12, true)
		m2, s2 := buildManifest(2, 0x02, 10, true)
		got := pipeline(t, []*frame.ShardManifest{m1, m2}, []netip.Addr{s1, s2})
		da := got.Domains[0x1]
		if da.QuorumMet || da.ShardBits != 0 || !da.Divergent {
			t.Fatalf("divergence not gated: %+v", da)
		}
		found := false
		for _, f := range got.DivergenceFields {
			if f == "domain_1_shard_bits" {
				found = true
			}
		}
		if !found {
			t.Fatalf("DivergenceFields = %v", got.DivergenceFields)
		}
	})

	t.Run("BRC-139-only manifests are unaffected", func(t *testing.T) {
		m1, s1 := buildManifest(1, 0x01, 0, false)
		m2, s2 := buildManifest(2, 0x02, 0, false)
		got := pipeline(t, []*frame.ShardManifest{m1, m2}, []netip.Addr{s1, s2})
		if len(got.Domains) != 0 {
			t.Fatalf("Domains adopted without descriptors: %+v", got.Domains)
		}
		if got.ShardBits != 8 {
			t.Fatalf("top-level adoption broken: %d", got.ShardBits)
		}
	})
}
