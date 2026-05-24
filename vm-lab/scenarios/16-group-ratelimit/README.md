# Scenario 16 — Per-Group Retransmit Rate Limit

**Goal:** verify two properties of the group-level (post-lookup) rate limiter:

1. `bre_rate_limit_drops_total{level="group"}` fires when cache-hitting NACKs
   exceed `RL_GROUP_RATE` per `(srcIP, groupIdx)`.

2. **ACK is still sent even when the retransmit is throttled.** The group
   limiter suppresses the re-multicast but returns an honest ACK so the listener
   cancels the gap immediately rather than escalating to the next endpoint.

   Observable as:
   ```
   delta(bre_responses_sent_total{type="ack"}) > delta(bre_retransmits_total)
   ```
   The difference is the count of group-throttled requests that still got ACK.

## Why this is distinct from scenarios 12–15

| Scenario | Tier exercised | Limiter position |
|---|---|---|
| 12 | IP + Sequence | Pre-lookup |
| 14 | IP (multi-endpoint) | Pre-lookup |
| 15 | Chain | Pre-lookup |
| **16** | **Group** | **Post-lookup (cache hit path only)** |

The group limiter only fires after a successful cache lookup. Pre-lookup
limiters drop the entire request (no response). The group limiter drops the
**retransmit** but preserves the **ACK** — a critical semantic difference.

## Rate limiting config (injected)

| Var | Scenario value | Production default |
|---|---|---|
| `RL_IP_RATE` | 50 000 | 1 000 |
| `RL_IP_BURST` | 10 000 | 100 |
| `RL_CHAIN_RATE` | 10 000 | 500 |
| `RL_CHAIN_WINDOW` | 60s | 1m |
| `RL_SEQUENCE_MAX` | 1 000 | 100 |
| `RL_GROUP_RATE` | **2** | 200 |
| `RL_GROUP_BURST` | **2** | 50 |

High IP, chain, and sequence limits ensure only the group tier fires.
`RL_GROUP_BURST=2` means the first two hits per (srcIP, groupIdx) succeed;
all subsequent hits within the token-bucket replenishment window are throttled.

## Gap injection design

Short `SEQ_GAP_DELAY=500ms` ensures frames ARE in cache when the NACK arrives
(`cache_ttl=60s`). This routes NACKs through the full post-lookup path. If
`gap_delay >= cache_ttl`, NACKs would miss the cache and the group limiter
would never be reached.

```
subtx-gen → gap (delay 500ms) → proxy caches frame
listeners detect gap → NACK → retry1 lookup → cache HIT → group limiter
```

## Assertions

| Metric | Filter | Expectation | Notes |
|---|---|---|---|
| `bre_cache_hits_total` | — | > 0 | Confirms post-lookup path reached |
| `bre_rate_limit_drops_total` | `level="group"` | **> 0** | **Core** |
| `bre_responses_sent_total` | `type="ack"` | **> bre_retransmits_total** | ACK preserved on throttle |
| `bre_rate_limit_drops_total` | `level="ip"` | ~0 | IP limiter cold |
| `bre_rate_limit_drops_total` | `level="chain"` | ~0 | Chain limiter cold |

## Tunables (env)

| Var | Default | Note |
|---|---|---|
| `PPS` | 500 | |
| `DURATION` | 15s | |
| `SEQ_GAP_EVERY` | 10 | Dense gaps → many NACKs |
| `SEQ_GAP_SIZE` | 3 | |
| `SEQ_GAP_DELAY` | 500ms | Must be < cache_ttl (60s) |
| `RL_GROUP_RATE` | 2 | tokens/s |
| `RL_GROUP_BURST` | 2 | |

## Prerequisites

- retry1 deployed and healthy.
- Listeners configured with retry1 in `retry_endpoints`.
- `SHARD_BITS=2` (default) — determines the `groupIdx` key space.

## Run

```bash
bash ~/repo/bitcoin-multicast-test/scenarios/16-group-ratelimit/run.sh
```
