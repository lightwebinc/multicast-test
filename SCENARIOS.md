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
| 04  | Extended dashboard (24h soak)                | `TestScenario04_ExtendedDashboard`           | [harness](harness/scenarios/scenario04_test.go)            |
| 05  | Multicast egress bridge (group re-mapping)   | `TestScenario05_McEgressBridge`              | [harness](harness/scenarios/scenario05_test.go)              |
| 06  | Functional BRC-128 (Extended Format payload) | `TestScenario06_FunctionalBRC128`            | [harness](harness/scenarios/scenario06_test.go)             |
| 07  | Functional BRC-128 + BRC-124 coexistence     | `TestScenario07_FunctionalBRC128Mixed`       | [harness](harness/scenarios/scenario07_test.go)       |
| 08  | NACK retransmit with BRC-128 payloads        | `TestScenario08_NackRetransmitBRC128`        | [harness](harness/scenarios/scenario08_test.go)        |
| 09  | Listener payload hash verification           | `TestScenario09_ListenerPayloadVerification` | [harness](harness/scenarios/scenario09_test.go) |

## 10–16 — NACK / retransmit / rate limiting

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

## 20–21 — Subtree group announce (BRC-127)

| #   | Title                                             | Test                                  | Files                                                                                                  |
| --- | ------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| 20  | BRC-127 subtree group announce: dynamic filtering | `TestScenario20_SubtreeGroupAnnounce` | [harness](harness/scenarios/scenario20_test.go) |
| 21  | Subtree group ramp (24.5h ramp test)              | `TestScenario21_SubtreeGroupRamp`     | [harness](harness/scenarios/scenario21_test.go)     |

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
| 34  | BRC-132 subtree data: NACK retransmission            | `TestScenario34_SubtreeDataRetransmit`    | [harness](harness/scenarios/scenario34_test.go)    |
| 35  | Block header egress: stripped BRC-131 retransmission | `TestScenario35_BlockHeaderEgress`        | [harness](harness/scenarios/scenario35_test.go)        |
| 36  | BRC-134 anchor frame: basic delivery                 | `TestScenario36_AnchorDelivery`           | [harness](harness/scenarios/scenario36_test.go)            |
| 37  | BRC-134 anchor frame: NACK retransmission            | `TestScenario37_AnchorRetransmit`         | [harness](harness/scenarios/scenario37_test.go)          |

## 40–42 — BGP ingress

`make test-bgp`

| #   | Title                                            | Test                                  | Files                                                                                                   |
| --- | ------------------------------------------------ | ------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| 40  | BGP ingress announce: AnyCast prefix propagation | `TestScenario40_BGPIngressAnnounce`   | [harness](harness/scenarios/scenario40_test.go)    |
| 41  | BGP ingress failover (stub)                      | `TestScenario41_BGPIngressFailover`   | [harness](harness/scenarios/scenario41_test.go)    |
| 42  | BGP multi-proxy anycast: ECMP + failover         | `TestScenario42_BGPMultiProxyAnycast` | [harness](harness/scenarios/scenario42_test.go) |

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

## 74 — Cross-domain NACK proxying

| #  | Title                          | Test                                  | Files                                                          |
| -- | ------------------------------ | ------------------------------------- | -------------------------------------------------------------- |
| 74 | Cross-domain NACK proxying     | `TestScenario74_NACKProxyCrossDomain` | [harness](harness/scenarios/scenario74_test.go) · harness only |

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

## 80–89 — Multicast mesh (ip6gre fabric) — moved to private ops

Scenarios 80–83 (ip6gre mesh replication, collapsed-mesh full-duplex demo,
WireGuard admin overlay, consumer-edge scale-out) and their privileged netns
repro scripts now live in a private repo. They were removed from this public
harness. The 80–89 decade stays **reserved** for mesh scenarios so the numbering
registry never collides. The transport they exercise is the
[integrated-infra `mc-router` role](https://github.com/lightwebinc/integrated-infra/blob/main/docs/mesh.md).

## 90–91 — BRC-142 coalescing (bundle frame)

Validates the [BRC-142 coalescing frame](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/brc-142-coalescing-frame.md)
(bundle format, `FrameVer 0x08`) — origin-side packing to cut fabric pps.

| #  | Title                          | Test                               | Files                                                                          |
| -- | ------------------------------ | ---------------------------------- | ------------------------------------------------------------------------------ |
| 90 | Coalesce → decoalesce delivery | `TestScenario90_CoalesceDelivery`  | [harness](harness/scenarios/scenario90_test.go) · proxy `COALESCE` → listener  |
| 91 | Coalesce loss / NACK recovery  | `TestScenario91_CoalesceLossRecovery` | [harness](harness/scenarios/scenario91_test.go) · bundle-unit retry recovery |

Scenario 90 enables `-coalesce` on the proxy over a shard-dense flow and proves
the proxy packs many small transactions into bundle datagrams (FrameVer 0x08,
`bsp_coalesce_*`) while the listener edge-decoalesces every member back out
(100% delivery — coalescing conserves transactions). Scenario 91 induces
multicast loss and proves bundle-unit recovery: the listener gap-tracks the
bundle SeqNum stream and NACKs, the retry endpoint serves the cached bundle
whole and retransmits it, and the re-decoalesced bundle closes the gap.

## 99 — End-to-end smoke

| #   | Title                      | Test                            | Files                                                                                           |
| --- | -------------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------- |
| 99  | NACK retransmit end-to-end | `TestScenario99_NACKRetransmit` | [harness](harness/scenarios/scenario99_test.go) |

## Make targets

| Target                       | Filter                          | Notes                          |
| ---------------------------- | ------------------------------- | ------------------------------ |
| `make test`                  | all                             | ~30 min, all harness scenarios |
| `make test-quick`            | `Scenario0[1-3]\|Scenario0[67]` | tier-1 filter scenarios (~60s) |
| `make test-retransmit`       | `Scenario(99\|1[0-6])`          | NACK / retransmit              |
| `make test-frag`             | `Scenario2[2-6]`                | fragmentation                  |
| `make test-bgp`              | `Scenario4[02]`                 | BGP                            |
| `make test-ssm`              | `Scenario6[01]`                 | SSM (RFC 4607)                 |
| `make test-manifest`         | `Scenario7[0-2]`                | BRC-139 / auto-shard-config    |
| `make test-coalesce`         | `Scenario9[01]`                 | BRC-142 coalescing / bundle    |
| `make test-one T=ScenarioNN` | single                          | run one scenario               |
