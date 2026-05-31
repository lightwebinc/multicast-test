# Scenario Index

Quick reference for the scenario tests under `harness/scenarios/`. Each row
points to its Go test file and the matching bash variant under
`vm-lab/scenarios/`.

Run a single scenario:

```bash
sudo go test ./harness/scenarios/... -v -run TestScenarioNN
# or
make test-one T=ScenarioNN
```

## 00 — Environment

| #   | Title                                         | Test                      | Files                                                                                    |
| --- | --------------------------------------------- | ------------------------- | ---------------------------------------------------------------------------------------- |
| 00  | Firewall rules validation (LXD-only, skipped) | `TestScenario00_Firewall` | [harness](harness/scenarios/scenario00_test.go) · [vm-lab](vm-lab/scenarios/00-firewall) |

## 01–09 — Functional / filters / payload

| #   | Title                                        | Test                                         | Files                                                                                                         |
| --- | -------------------------------------------- | -------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| 01  | Functional all-shards                        | `TestScenario01_FunctionalAllShards`         | [harness](harness/scenarios/scenario01_test.go) · [vm-lab](vm-lab/scenarios/01-functional-all-shards)         |
| 02  | Functional shard filter                      | `TestScenario02_FunctionalShardFilter`       | [harness](harness/scenarios/scenario02_test.go) · [vm-lab](vm-lab/scenarios/02-functional-shard-filter)       |
| 03  | Functional subtree filter                    | `TestScenario03_FunctionalSubtreeFilter`     | [harness](harness/scenarios/scenario03_test.go) · [vm-lab](vm-lab/scenarios/03-functional-subtree-filter)     |
| 04  | Extended dashboard (24h soak)                | `TestScenario04_ExtendedDashboard`           | [harness](harness/scenarios/scenario04_test.go) · [vm-lab](vm-lab/scenarios/04-extended-dashboard)            |
| 05  | Multicast egress bridge (group re-mapping)   | `TestScenario05_McEgressBridge`              | [harness](harness/scenarios/scenario05_test.go) · [vm-lab](vm-lab/scenarios/05-mc-egress-bridge)              |
| 06  | Functional BRC-128 (Extended Format payload) | `TestScenario06_FunctionalBRC128`            | [harness](harness/scenarios/scenario06_test.go) · [vm-lab](vm-lab/scenarios/06-functional-brc128)             |
| 07  | Functional BRC-128 + BRC-124 coexistence     | `TestScenario07_FunctionalBRC128Mixed`       | [harness](harness/scenarios/scenario07_test.go) · [vm-lab](vm-lab/scenarios/07-functional-brc128-mixed)       |
| 08  | NACK retransmit with BRC-128 payloads        | `TestScenario08_NackRetransmitBRC128`        | [harness](harness/scenarios/scenario08_test.go) · [vm-lab](vm-lab/scenarios/08-nack-retransmit-brc128)        |
| 09  | Listener payload hash verification           | `TestScenario09_ListenerPayloadVerification` | [harness](harness/scenarios/scenario09_test.go) · [vm-lab](vm-lab/scenarios/09-listener-payload-verification) |

## 10–16 — NACK / retransmit / rate limiting

`make test-retransmit`

| #   | Title                                           | Test                                    | Files                                                                                                    |
| --- | ----------------------------------------------- | --------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| 10  | Single endpoint ACK (tightened)                 | `TestScenario10_SingleEndpointACK`      | [harness](harness/scenarios/scenario10_test.go) · [vm-lab](vm-lab/scenarios/10-single-endpoint-ack)      |
| 11  | Permanent gap / MISS (cache-empty)              | `TestScenario11_PermanentGapMISS`       | [harness](harness/scenarios/scenario11_test.go) · [vm-lab](vm-lab/scenarios/11-permanent-gap-miss)       |
| 12  | Burst gap + rate limiting                       | `TestScenario12_BurstGapRatelimit`      | [harness](harness/scenarios/scenario12_test.go) · [vm-lab](vm-lab/scenarios/12-burst-gap-ratelimit)      |
| 13  | MISS escalation by tier                         | `TestScenario13_MissEscalationTier`     | [harness](harness/scenarios/scenario13_test.go) · [vm-lab](vm-lab/scenarios/13-miss-escalation-tier)     |
| 14  | Multi-endpoint rate limit defense               | `TestScenario14_MultiEndpointRatelimit` | [harness](harness/scenarios/scenario14_test.go) · [vm-lab](vm-lab/scenarios/14-multi-endpoint-ratelimit) |
| 15  | Per-chain NACK rate limit                       | `TestScenario15_ChainRatelimit`         | [harness](harness/scenarios/scenario15_test.go) · [vm-lab](vm-lab/scenarios/15-chain-ratelimit)          |
| 16  | Per-group retransmit rate limit (ACK preserved) | `TestScenario16_GroupRatelimit`         | [harness](harness/scenarios/scenario16_test.go) · [vm-lab](vm-lab/scenarios/16-group-ratelimit)          |

## 20–21 — Subtree group announce (BRC-127)

| #   | Title                                             | Test                                  | Files                                                                                                  |
| --- | ------------------------------------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| 20  | BRC-127 subtree group announce: dynamic filtering | `TestScenario20_SubtreeGroupAnnounce` | [harness](harness/scenarios/scenario20_test.go) · [vm-lab](vm-lab/scenarios/20-subtree-group-announce) |
| 21  | Subtree group ramp (24.5h ramp test)              | `TestScenario21_SubtreeGroupRamp`     | [harness](harness/scenarios/scenario21_test.go) · [vm-lab](vm-lab/scenarios/21-subtree-group-ramp)     |

## 22–26 — Fragmentation (BRC-130)

`make test-frag`

| #   | Title                                                 | Test                                      | Files                                                                                                      |
| --- | ----------------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| 22  | Fragmentation: basic delivery                         | `TestScenario22_FragmentationDelivery`    | [harness](harness/scenarios/scenario22_test.go) · [vm-lab](vm-lab/scenarios/22-fragmentation-delivery)     |
| 23  | Fragmentation: shard filter                           | `TestScenario23_FragmentationShardFilter` | [harness](harness/scenarios/scenario23_test.go) · [vm-lab](vm-lab/scenarios/23-fragmentation-shard-filter) |
| 24  | Fragmentation: payload hash verification              | `TestScenario24_FragmentationHashVerify`  | [harness](harness/scenarios/scenario24_test.go) · [vm-lab](vm-lab/scenarios/24-fragmentation-hash-verify)  |
| 25  | Fragmentation: fragment loss / reassembly abandonment | `TestScenario25_FragmentationLoss`        | [harness](harness/scenarios/scenario25_test.go) · [vm-lab](vm-lab/scenarios/25-fragmentation-loss)         |
| 26  | Fragmentation: high-throughput delivery ratio         | `TestScenario26_FragmentationThroughput`  | [harness](harness/scenarios/scenario26_test.go) · [vm-lab](vm-lab/scenarios/26-fragmentation-throughput)   |

## 30–37 — Block / subtree / anchor (BRC-131, BRC-132, BRC-134)

| #   | Title                                                | Test                                      | Files                                                                                                      |
| --- | ---------------------------------------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| 30  | BRC-131 block announcement: basic delivery           | `TestScenario30_BlockAnnounceDelivery`    | [harness](harness/scenarios/scenario30_test.go) · [vm-lab](vm-lab/scenarios/30-block-announce-delivery)    |
| 31  | BRC-131 block announcement: NACK retransmission      | `TestScenario31_BlockAnnounceRetransmit`  | [harness](harness/scenarios/scenario31_test.go) · [vm-lab](vm-lab/scenarios/31-block-announce-retransmit)  |
| 32  | BRC-132 subtree data: basic delivery                 | `TestScenario32_SubtreeDataDelivery`      | [harness](harness/scenarios/scenario32_test.go) · [vm-lab](vm-lab/scenarios/32-subtree-data-delivery)      |
| 33  | BRC-132 subtree data: fragmentation                  | `TestScenario33_SubtreeDataFragmentation` | [harness](harness/scenarios/scenario33_test.go) · [vm-lab](vm-lab/scenarios/33-subtree-data-fragmentation) |
| 34  | BRC-132 subtree data: NACK retransmission            | `TestScenario34_SubtreeDataRetransmit`    | [harness](harness/scenarios/scenario34_test.go) · [vm-lab](vm-lab/scenarios/34-subtree-data-retransmit)    |
| 35  | Block header egress: stripped BRC-131 retransmission | `TestScenario35_BlockHeaderEgress`        | [harness](harness/scenarios/scenario35_test.go) · [vm-lab](vm-lab/scenarios/35-block-header-egress)        |
| 36  | BRC-134 anchor frame: basic delivery                 | `TestScenario36_AnchorDelivery`           | [harness](harness/scenarios/scenario36_test.go) · [vm-lab](vm-lab/scenarios/36-anchor-delivery)            |
| 37  | BRC-134 anchor frame: NACK retransmission            | `TestScenario37_AnchorRetransmit`         | [harness](harness/scenarios/scenario37_test.go) · [vm-lab](vm-lab/scenarios/37-anchor-retransmit)          |

## 40–42 — BGP ingress

`make test-bgp`

| #   | Title                                            | Test                                  | Files                                                                                                   |
| --- | ------------------------------------------------ | ------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| 40  | BGP ingress announce: AnyCast prefix propagation | `TestScenario40_BGPIngressAnnounce`   | [harness](harness/scenarios/scenario40_test.go) · [vm-lab](vm-lab/scenarios/40-bgp-ingress-announce)    |
| 41  | BGP ingress failover (stub)                      | `TestScenario41_BGPIngressFailover`   | [harness](harness/scenarios/scenario41_test.go) · [vm-lab](vm-lab/scenarios/41-bgp-ingress-failover)    |
| 42  | BGP multi-proxy anycast: ECMP + failover         | `TestScenario42_BGPMultiProxyAnycast` | [harness](harness/scenarios/scenario42_test.go) · [vm-lab](vm-lab/scenarios/42-bgp-multi-proxy-anycast) |

## 50–53 — TxID dedup

| #   | Title                                                      | Test                                   | Files                                                                                                    |
| --- | ---------------------------------------------------------- | -------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| 50  | TxID dedup basic: cross-listener deduplication             | `TestScenario50_TxIDDedupBasic`        | [harness](harness/scenarios/scenario50_test.go) · [vm-lab](vm-lab/scenarios/50-txid-dedup-basic)         |
| 51  | TxID dedup: collision (single listener, no dedup expected) | `TestScenario51_TxIDDedupCollision`    | [harness](harness/scenarios/scenario51_test.go) · [vm-lab](vm-lab/scenarios/51-txid-dedup-collision)     |
| 52  | TxID dedup: Redis failure (fail-open)                      | `TestScenario52_TxIDDedupRedisFailure` | [harness](harness/scenarios/scenario52_test.go) · [vm-lab](vm-lab/scenarios/52-txid-dedup-redis-failure) |
| 53  | TxID dedup: sentinel failover                              | `TestScenario53_TxIDDedupFailover`     | [harness](harness/scenarios/scenario53_test.go) · [vm-lab](vm-lab/scenarios/53-txid-dedup-failover)      |

## 60–69 — Source-Specific Multicast (RFC 4607)

`make test-ssm`

Validates the [SSM Support Plan](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/SourceSpecificMulticast/ssm-support-plan.md)
implementation. Full Posture C cross-host SSM data delivery requires
PIM-SSM in the fabric (not provided by Docker's default bridge) and
is validated on real fabric hosts; no vm-lab variants.

| #   | Title                                         | Test                              | Files                                                          |
| --- | --------------------------------------------- | --------------------------------- | -------------------------------------------------------------- |
| 60  | SSM loopback Join/Leave (kernel sanity check) | `TestScenario60_SSMLoopback`      | [harness](harness/scenarios/scenario60_test.go) · harness only |
| 61  | SSM ASM-fallback startup (scaffolding no-op)  | `TestScenario61_SSMASMFallback`   | [harness](harness/scenarios/scenario61_test.go) · harness only |

## 99 — End-to-end smoke

| #   | Title                      | Test                            | Files                                                                                           |
| --- | -------------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------- |
| 99  | NACK retransmit end-to-end | `TestScenario99_NACKRetransmit` | [harness](harness/scenarios/scenario99_test.go) · [vm-lab](vm-lab/scenarios/99-nack-retransmit) |

## Make targets

| Target                       | Filter                          | Notes                          |
| ---------------------------- | ------------------------------- | ------------------------------ |
| `make test`                  | all                             | ~30 min, all 40 scenarios      |
| `make test-quick`            | `Scenario0[1-3]\|Scenario0[67]` | tier-1 filter scenarios (~60s) |
| `make test-retransmit`       | `Scenario(99\|1[0-6])`          | NACK / retransmit              |
| `make test-frag`             | `Scenario2[2-6]`                | fragmentation                  |
| `make test-bgp`              | `Scenario4[02]`                 | BGP                            |
| `make test-ssm`              | `Scenario6[01]`                 | SSM (RFC 4607)                 |
| `make test-one T=ScenarioNN` | single                          | run one scenario               |
