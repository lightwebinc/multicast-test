# Scenario 15 — Per-Chain NACK Rate Limit

**Goal:** verify that the chain-level rate limiter (`bre_rate_limit_drops_total{level="chain"}`)
fires when a single source IP floods NACKs with the same `HashKey` (per-flow chain identifier)
at a rate exceeding `RL_CHAIN_RATE`, while `HashKey=0` (orphan/unattributed gaps) bypasses
the chain limiter entirely.

## Why a dedicated scenario

Scenario 14 proves the IP-level limiter using `HashKey=0` floods. Those NACKs
intentionally bypass the chain limiter (orphan gaps must not share a bucket).
This scenario drives the other half: a flood with a **fixed non-zero `HashKey`**
that exhausts the per-`(srcIP, HashKey)` sliding window, and a parallel
control flood with `HashKey=0` that must not produce chain drops.

## Attack vectors

| Thread | Source | HashKey | StartSeq/EndSeq | Purpose |
|---|---|---|---|---|
| Chain flood | source VM (fd20::10) | `0xCAFEBABEDEAD0001` | fixed | Exhaust per-chain window |
| Orphan control | source VM (fd20::10) | `0` | fixed, different | Verify orphan bypass |
| Legitimate gaps | subtx-gen → listeners | real per-flow XXH64 | real seq | Prove end-to-end per-flow chain attribution |

## Rate limiting config (injected)

| Var | Scenario value | Production default |
|---|---|---|
| `RL_IP_RATE` | 50 000 | 1 000 |
| `RL_IP_BURST` | 10 000 | 100 |
| `RL_CHAIN_RATE` | 3 per window | 500 |
| `RL_CHAIN_WINDOW` | 10s | 1m |
| `RL_SEQUENCE_MAX` | 1 000 | 100 |
| `RL_GROUP_RATE` | 10 000 | 200 |
| `RL_GROUP_BURST` | 5 000 | 50 |

High IP and group limits ensure only the chain tier fires.

## NACK wire format (64 bytes — see BRC-126)

```text
[0:4]   Magic      0xE3E1F3E8
[4:6]   ProtoVer   0x02BF
[6]     MsgType    0x10  (NACK)
[7]     Flags      0x00
[8:16]  HashKey    uint64 BE  ← per-flow identifier; 0 = orphan (bypasses chain/HashKey limiter)
[16:24] StartSeq   uint64 BE
[24:32] EndSeq     uint64 BE  (== StartSeq for single-frame)
[32:64] SubtreeID  32 bytes
```

The per-chain rate limit is keyed on `HashKey` (the stable per-flow identifier).
A fixed non-zero `HashKey` from a flood exhausts the per-chain window for that
flow; `HashKey=0` is treated as orphan and bypasses the chain limiter.

## Assertions

| Counter | Filter | Endpoint | Expectation |
|---|---|---|---|
| `bre_nack_requests_total` | — | retry1 | > 0 |
| `bre_rate_limit_drops_total` | `level="chain"` | **retry1** | **> 0** (core) |
| `bre_rate_limit_drops_total` | `level="ip"` | retry1 | ~0 (IP limiter cold) |

## Tunables (env)

| Var | Default |
|---|---|
| `PPS` | 200 |
| `DURATION` | 15s |
| `SEQ_GAP_EVERY` | 30 |
| `SEQ_GAP_SIZE` | 2 |
| `SEQ_GAP_DELAY` | 1s |
| `RL_CHAIN_RATE` | 3 |
| `RL_CHAIN_WINDOW` | 10s |

## Prerequisites

- retry1 deployed and healthy.
- Listeners configured with retry1 in `retry_endpoints`.
- Python3 on source VM.

## Run

```bash
bash ~/repo/bitcoin-multicast-test/scenarios/15-chain-ratelimit/run.sh
```
