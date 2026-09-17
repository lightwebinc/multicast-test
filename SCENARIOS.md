# Scenario Index

Quick reference for the scenario tests under `harness/scenarios/`. Each row
points to its Go test file.

Run a single scenario:

```bash
sudo go test ./harness/scenarios/... -v -run TestScenarioNN
# or
make test-one T=ScenarioNN
```

## 00 — Environment

| #   | Title                                         | Test                      | Files                                                                                    |
| --- | --------------------------------------------- | ------------------------- | ---------------------------------------------------------------------------------------- |
| 00  | Firewall rules validation (LXD-only, skipped) | `TestScenario00_Firewall` | [harness](harness/scenarios/scenario00_test.go) |

## 01–09 — Functional / filters / payload

| #   | Title                                        | Test                                         | Files                                                                                                         |
| --- | -------------------------------------------- | -------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| 01  | Functional all-shards                        | `TestScenario01_FunctionalAllShards`         | [harness](harness/scenarios/scenario01_test.go)         |
| 02  | Functional shard filter                      | `TestScenario02_FunctionalShardFilter`       | [harness](harness/scenarios/scenario02_test.go)       |
| 03  | Functional subtree filter                    | `TestScenario03_FunctionalSubtreeFilter`     | [harness](harness/scenarios/scenario03_test.go)     |
| 04  | Extended dashboard (24h soak, skipped)       | `TestScenario04_ExtendedDashboard`           | [harness](harness/scenarios/scenario04_test.go)            |
| 05  | Multicast egress bridge (group re-mapping)   | `TestScenario05_McEgressBridge`              | [harness](harness/scenarios/scenario05_test.go)              |
| 06  | Functional BRC-128 (Extended Format payload) | `TestScenario06_FunctionalBRC128`            | [harness](harness/scenarios/scenario06_test.go)             |
| 07  | Functional BRC-128 + BRC-124 coexistence     | `TestScenario07_FunctionalBRC128Mixed`       | [harness](harness/scenarios/scenario07_test.go)       |
| 08  | NACK retransmit with BRC-128 payloads        | `TestScenario08_NackRetransmitBRC128`        | [harness](harness/scenarios/scenario08_test.go)        |
| 09  | Listener payload hash verification           | `TestScenario09_ListenerPayloadVerification` | [harness](harness/scenarios/scenario09_test.go) |

## 10–19 — NACK / retransmit / rate limiting

`make test-retransmit`

| #   | Title                                           | Test                                    | Files                                                                                                    |
| --- | ----------------------------------------------- | --------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| 10  | Single endpoint ACK (tightened)                 | `TestScenario10_SingleEndpointACK`      | [harness](harness/scenarios/scenario10_test.go)      |
| 11  | Permanent gap / MISS (cache-empty)              | `TestScenario11_PermanentGapMISS`       | [harness](harness/scenarios/scenario11_test.go)       |
| 12  | Burst gap + rate limiting                       | `TestScenario12_BurstGapRatelimit`      | [harness](harness/scenarios/scenario12_test.go)      |
| 13  | MISS escalation by tier                         | `TestScenario13_MissEscalationTier`     | [harness](harness/scenarios/scenario13_test.go)     |
| 14  | Multi-endpoint rate limit defense               | `TestScenario14_MultiEndpointRatelimit` | [harness](harness/scenarios/scenario14_test.go) |
| 15  | Per-chain NACK rate limit                       | `TestScenario15_ChainRatelimit`         | [harness](harness/scenarios/scenario15_test.go)          |
| 16  | Per-group retransmit rate limit (ACK preserved) | `TestScenario16_GroupRatelimit`         | [harness](harness/scenarios/scenario16_test.go)          |
| 17  | Recovery shortfall identity (clean regime)      | `TestScenario17_RecoveryShortfallIdentity` | [harness](harness/scenarios/scenario17_test.go) · full exact identity: `unrecovered == sent − delivered == 0` + gap closure per unfiltered listener (unicast-only repair, warmup/trailer). Found+fixed a real tracker defect: a low-seq repair falsely tripped the proxy-restart flow reset (phantom unrecovered cascade) |
| 18  | Unrecoverable-loss (cache MISS) shortfall identity | `TestScenario18_UnrecoverableLossShortfallIdentity` | [harness](harness/scenarios/scenario18_test.go) · same identity with `unrecovered > 0` forced by an empty retry cache (rate-limit starvation only DELAYS repair — a late range-fill still delivers, breaking exactness) |
| 19  | Listener retry-tee feeds a join-less cache      | `TestScenario19_ListenerTeeFedJoinlessRepair` | [harness](harness/scenarios/scenario19_test.go) · retry runs `MC_JOIN_ENABLED=false` + `TEE_LISTEN`, fed ONLY by a co-resident listener's `-retry-tee` (real shared netns via `ShareNetNS`); remote listeners' netem loss fully repaired from that cache, with per-source attribution preserved through the teewire envelope (`bre_cache_stored_total{source=<proxy>}` moves, `{source="::1"}` stays 0) |

## 20–21 — Subtree group announce (BRC-127)

| #   | Title                                             | Test                                  | Files                                                                                                  |
| --- | ------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| 20  | BRC-127 subtree group announce: dynamic filtering | `TestScenario20_SubtreeGroupAnnounce` | [harness](harness/scenarios/scenario20_test.go) |
| 21  | Subtree group ramp (24.5h ramp, skipped)          | `TestScenario21_SubtreeGroupRamp`     | [harness](harness/scenarios/scenario21_test.go)     |

## 22–26 — Fragmentation (BRC-130)

`make test-frag`

| #   | Title                                                 | Test                                      | Files                                                                                                      |
| --- | ----------------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| 22  | Fragmentation: basic delivery                         | `TestScenario22_FragmentationDelivery`    | [harness](harness/scenarios/scenario22_test.go)     |
| 23  | Fragmentation: shard filter                           | `TestScenario23_FragmentationShardFilter` | [harness](harness/scenarios/scenario23_test.go) |
| 24  | Fragmentation: payload hash verification              | `TestScenario24_FragmentationHashVerify`  | [harness](harness/scenarios/scenario24_test.go)  |
| 25  | Fragmentation: fragment loss / reassembly abandonment | `TestScenario25_FragmentationLoss`        | [harness](harness/scenarios/scenario25_test.go)         |
| 26  | Fragmentation: high-throughput delivery ratio         | `TestScenario26_FragmentationThroughput`  | [harness](harness/scenarios/scenario26_test.go)   |

## 30–37 — Block / subtree / anchor (BRC-131, BRC-132, BRC-134)

| #   | Title                                                | Test                                      | Files                                                                                                      |
| --- | ---------------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| 30  | BRC-131 block announcement: basic delivery           | `TestScenario30_BlockAnnounceDelivery`    | [harness](harness/scenarios/scenario30_test.go)    |
| 31  | BRC-131 block announcement: NACK retransmission      | `TestScenario31_BlockAnnounceRetransmit`  | [harness](harness/scenarios/scenario31_test.go)  |
| 32  | BRC-132 subtree data: basic delivery                 | `TestScenario32_SubtreeDataDelivery`      | [harness](harness/scenarios/scenario32_test.go)      |
| 33  | BRC-132 subtree data: fragmentation                  | `TestScenario33_SubtreeDataFragmentation` | [harness](harness/scenarios/scenario33_test.go) |
| 34  | BRC-132 subtree data: fragment-level NACK retransmission | `TestScenario34_SubtreeDataRetransmit`    | [harness](harness/scenarios/scenario34_test.go)    |
| 35  | Block header egress (via the 8727 block push lane)   | `TestScenario35_BlockHeaderEgress`        | [harness](harness/scenarios/scenario35_test.go)        |
| 36  | BRC-134 anchor frame: basic delivery                 | `TestScenario36_AnchorDelivery`           | [harness](harness/scenarios/scenario36_test.go)            |
| 37  | BRC-134 anchor frame: NACK retransmission            | `TestScenario37_AnchorRetransmit`         | [harness](harness/scenarios/scenario37_test.go)          |

## 40–42 — BGP ingress

`make test-bgp`

All three BGP scenarios currently skip: 40 and 42 are deferred pending FRR +
BIRD2 sidecar containers and a multi-network topology, and 41 is a stub —
`make test-bgp` runs no real tests yet.

| #   | Title                                                       | Test                                  | Files                                                                                                   |
| --- | ----------------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| 40  | BGP ingress announce: AnyCast prefix propagation (deferred) | `TestScenario40_BGPIngressAnnounce`   | [harness](harness/scenarios/scenario40_test.go)    |
| 41  | BGP ingress failover (stub)                                 | `TestScenario41_BGPIngressFailover`   | [harness](harness/scenarios/scenario41_test.go)    |
| 42  | BGP multi-proxy anycast: ECMP + failover (deferred)         | `TestScenario42_BGPMultiProxyAnycast` | [harness](harness/scenarios/scenario42_test.go) |

## 50–53 — TxID dedup

| #   | Title                                                      | Test                                   | Files                                                                                                    |
| --- | ---------------------------------------------------------- | -------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| 50  | TxID dedup basic: cross-listener deduplication             | `TestScenario50_TxIDDedupBasic`        | [harness](harness/scenarios/scenario50_test.go)         |
| 51  | TxID dedup: collision (single listener, no dedup expected) | `TestScenario51_TxIDDedupCollision`    | [harness](harness/scenarios/scenario51_test.go)     |
| 52  | TxID dedup: Redis failure (fail-open)                      | `TestScenario52_TxIDDedupRedisFailure` | [harness](harness/scenarios/scenario52_test.go) |
| 53  | TxID dedup: sentinel failover                              | `TestScenario53_TxIDDedupFailover`     | [harness](harness/scenarios/scenario53_test.go)      |

## 60–69 — Source-Specific Multicast (RFC 4607)

`make test-ssm`

Validates the [SSM Support Plan](https://github.com/lightwebinc/bsv-multicast/blob/main/DESIGN.md#source-specific-multicast-ssm)
implementation. Full Posture C cross-host SSM data delivery requires
PIM-SSM in the fabric (not provided by Docker's default bridge) and
is validated on real fabric hosts; no vm-lab variants.

| #   | Title                                         | Test                              | Files                                                          |
| --- | --------------------------------------------- | --------------------------------- | -------------------------------------------------------------- |
| 60  | SSM loopback Join/Leave (kernel sanity check) | `TestScenario60_SSMLoopback`      | [harness](harness/scenarios/scenario60_test.go) · harness only |
| 61  | SSM ASM-fallback startup (scaffolding no-op)  | `TestScenario61_SSMASMFallback`   | [harness](harness/scenarios/scenario61_test.go) · harness only |

## 70–79 — BRC-139 manifest / auto-shard-config

| #  | Title                                       | Test                                       | Files                                                          |
| -- | ------------------------------------------- | ------------------------------------------ | -------------------------------------------------------------- |
| 70 | BRC-139 wire-level manifest pipeline        | `TestScenario70_BRC139WirePipeline`        | [harness](harness/scenarios/scenario70_test.go) · harness only |
| 71 | BRC-139 Successor block live-reshard signal | `TestScenario71_BRC139SuccessorSignal`     | [harness](harness/scenarios/scenario70_test.go) · harness only |
| 72 | BRC-139 adoption safety gates               | `TestScenario72_BRC139AdoptionSafetyGates` | [harness](harness/scenarios/scenario72_test.go) · harness only |

## 73 — Unified logging

| #  | Title                                | Test                                    | Files                                                          |
| -- | ------------------------------------ | --------------------------------------- | -------------------------------------------------------------- |
| 73 | Unified logging emit contract (e2e)  | `TestScenario73_UnifiedLoggingContract` | [harness](harness/scenarios/scenario73_test.go) · no fabric    |

Builds and runs the real `shard-manifest` binary with `LOG_FORMAT=json` and
asserts the [unified logging](https://github.com/lightwebinc/shard-common/blob/main/docs/logging.md)
emit contract: one JSON object per line, the `service.{name,instance.id,version}`
identity triple on every line, and a single `host.inventory` event nesting
os/cpu/mem/net/build groups with both IPv4 and IPv6 address keys per interface.
Needs only `go` + loopback (no Docker fabric).

## 74–75 — Cross-domain NACK proxying

| #  | Title                          | Test                                  | Files                                                          |
| -- | ------------------------------ | ------------------------------------- | -------------------------------------------------------------- |
| 74 | Cross-domain NACK proxying     | `TestScenario74_NACKProxyCrossDomain` | [harness](harness/scenarios/scenario74_test.go) · harness only |
| 75 | NACK repair at inter-fabric RTT | `TestScenario75_NACKRepairInterFabric` | [harness](harness/scenarios/scenario75_test.go) · requires sudo (netem) |

Two domains on one fabric (isolated by mc-group-id): a proxy + upstream
retry-endpoint feed a bridge `shard-listener` whose multicast egress re-emits
into a downstream domain (consumer + downstream retry-endpoint). The bridge runs
with ingress netem loss and **no** retry config, so frames it never receives are
absent from the entire downstream domain — only the upstream retry has them. The
downstream retry (`PROXY_ENABLED`) recovers those misses from the upstream
endpoint via a `Proxied`-flagged NACK (unicast frame return), re-caches, and
multicast-retransmits into the downstream domain so the consumer's gap fills.
Asserts the full chain: downstream `bre_proxy_recovered_total`, upstream
`bre_unicast_retransmits_total`, and consumer `bsl_gaps_suppressed_total`. See
[BRC-126](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/brc-126-retransmission-protocol.md).

## 80–88 — Multicast mesh (ip6gre fabric) — not in this harness

Scenarios 80–83 (ip6gre mesh replication, collapsed-mesh full-duplex demo,
WireGuard admin overlay, consumer-edge scale-out) and their privileged netns
repro scripts are not part of this harness: they need a routed multi-host
fabric rather than the single-host bridge the suite builds. The 80–88 range
stays **reserved** for mesh scenarios so the numbering registry never
collides.

## 89–91 — BRC-142 coalescing (bundle frame)

Validates the [BRC-142 coalescing frame](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/brc-142-coalescing-frame.md)
(bundle format, `FrameVer 0x08`) — origin-side packing to cut fabric pps.

| #  | Title                          | Test                               | Files                                                                          |
| -- | ------------------------------ | ---------------------------------- | ------------------------------------------------------------------------------ |
| 89 | Re-bucket loss / parent recovery | `TestScenario89_RebucketLossRecovery` | [harness](harness/scenarios/scenario89_test.go) · generation-mismatch re-bucket + parent-stream NACK recovery + unguarded alarm |
| 90 | Coalesce → decoalesce delivery | `TestScenario90_CoalesceDelivery`  | [harness](harness/scenarios/scenario90_test.go) · proxy `COALESCE` → listener  |
| 91 | Coalesce loss / NACK recovery  | `TestScenario91_CoalesceLossRecovery` | [harness](harness/scenarios/scenario91_test.go) · bundle-unit retry recovery |

Scenario 90 enables `-coalesce` on the proxy over a shard-dense flow and proves
the proxy packs many small transactions into bundle datagrams (FrameVer 0x08,
`bsp_coalesce_*`) while the listener edge-decoalesces every member back out
(100% delivery — coalescing conserves transactions). Scenario 91 induces
multicast loss and proves bundle-unit recovery: the listener gap-tracks the
bundle SeqNum stream and NACKs, the retry endpoint serves the cached bundle
whole and retransmits it, and the re-decoalesced bundle closes the gap.

## 92–98 — BRC-148 BEEF object plane

Validates the [BRC-148 shard domain partition + BEEF object plane](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/brc-148-shard-domain-beef-plane.md)
(`FrameVer 0x09` on the `0x1000` band; topic-sharded, open ingress class):
submission-record grammar on the open tx port and the optional dedicated
lane, topic + version (encoding-capability) filtering, BRC-130 fragmentation,
BRC-126 recovery, and the per-domain BRC-139 manifest extension.
`make test-beef`.

| #  | Title                                   | Test                                    | Files                                           |
| -- | --------------------------------------- | --------------------------------------- | ----------------------------------------------- |
| 92 | Submit → deliver (open port + lane)     | `TestScenario92_BeefSubmitDeliver`      | [harness](harness/scenarios/scenario92_test.go) |
| 93 | Fragmented-BEEF loss recovery (fragment gap → NACK → retransmit) | `TestScenario93_BeefFragmentLossRecovery` | [harness](harness/scenarios/scenario93_test.go) |
| 93b | Same under **unicast-only** repair (the shipped ops posture: `beacon_flags_multicast=false`); asserts recovered fragments COMPLETE objects, not just that gaps were booked | `TestScenario93b_BeefFragmentLossRecoveryUnicast` | [harness](harness/scenarios/scenario93_test.go) |
| 94 | Version filter + verbatim carriage      | `TestScenario94_BeefVersionFilterVerbatim` | [harness](harness/scenarios/scenario94_test.go) |
| 95 | Large-object fragmentation              | `TestScenario95_BeefFragmentation`      | [harness](harness/scenarios/scenario95_test.go) |
| 96 | NACK recovery on the BEEF plane         | `TestScenario96_BeefNackRecovery`       | [harness](harness/scenarios/scenario96_test.go) |
| 97 | Per-domain manifest coordination        | `TestScenario97_BeefDomainAdoption`     | [harness](harness/scenarios/scenario97_test.go) · harness only |
| 98 | Plane independence (concurrent tx+BEEF) | `TestScenario98_PlaneIndependence`      | [harness](harness/scenarios/scenario98_test.go) |

The OSS BEEF harness covers the single-topic stance only; multi-topic fan-out
requires an authenticated submit policy and is tested elsewhere (the OSS proxy rejects a submission
record naming more than one topic — `bsp_beef_submissions_total{result="multi_topic"}`).
Scenario 94 includes the real BRC-62 specification
example with listener ContentID verification (byte-identical carriage).
Scenario 97 is in-process (wire encode → decode → registry → evaluator):
per-domain quorum/hysteresis/divergence with BRC-139-only back-compat.

## 99 — End-to-end smoke

| #   | Title                      | Test                            | Files                                                                                           |
| --- | -------------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------- |
| 99  | NACK retransmit end-to-end | `TestScenario99_NACKRetransmit` | [harness](harness/scenarios/scenario99_test.go) |

## Make targets

| Target                       | Filter                          | Notes                          |
| ---------------------------- | ------------------------------- | ------------------------------ |
| `make test`                  | all                             | ~30 min, all harness scenarios |
| `make test-quick`            | `Scenario0[1-3]\|Scenario0[67]` | tier-1 filter scenarios (~60s) |
| `make test-retransmit`       | `Scenario(99\|08\|1[0-9])`      | NACK / retransmit              |
| `make test-subtree-announce` | `Scenario2[01]`                 | BRC-127 subtree announce       |
| `make test-frag`             | `Scenario2[2-6]`                | fragmentation                  |
| `make test-block`            | `Scenario3[0-7]`                | BRC-131/132/134/135 lanes      |
| `make test-bgp`              | `Scenario4[02]`                 | BGP (all currently skip)       |
| `make test-dedup`            | `Scenario5[0-3]`                | TxID dedup                     |
| `make test-ssm`              | `Scenario6[01]`                 | SSM (RFC 4607)                 |
| `make test-manifest`         | `Scenario7[0-5]`                | BRC-139 / auto-shard-config    |
| `make test-coalesce`         | `Scenario(89\|9[01])`           | BRC-142 coalescing / bundle    |
| `make test-beef`             | `Scenario9[2-8]`                | BRC-148 BEEF object plane      |
| `make test-one T=ScenarioNN` | single                          | run one scenario               |

## Formerly-red scenarios (resolved 2026-07-24)

The 2026-07-24 BRC-148 work rebuilt the long-stale harness images (cached
from before 2026-07-07), exposing three pre-existing failures — since fixed:

- **05 (mc-egress bridge)** and the bridge half of **74 (cross-domain NACK
  proxy)** — mc-egress re-emitted payload-only frames because it honoured the
  `-strip-header` flag (default on). A bridged frame crosses into a domain of
  *listeners* that must decode the BRC-124 header to demux, gap-track, and
  NACK; the stripped payload was undecodable (`decode_error`), so the
  downstream listener received nothing / saw zero SeqNums. `MCastSender` now
  always forwards the complete frame; strip-header applies only to the final
  unicast hop to a raw-tx consumer.
- **91 (bundle loss recovery)** — the scenario ran the listeners at the default
  ShardBits while the proxy was pinned to 1, so each received bundle was
  re-bucketed and re-stamped from a *local* SeqNum counter that never
  incremented for an upstream-dropped bundle (loss invisible → zero gaps). The
  listeners now match the proxy's ShardBits, exercising the real bundle-unit
  recovery path (the retry caches and serves the proxy-stamped bundle).

(Scenario 34, previously skipped, now passes with fragment-level gap tracking.)

