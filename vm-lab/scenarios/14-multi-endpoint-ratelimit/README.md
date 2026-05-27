# Scenario 14 — Multi-Endpoint Rate Limit Defense

**Goal:** verify that rate limiting provides network-wide protection by proving
all three retry endpoints independently throttle a NACK flood regardless of
the attack source, covering three simultaneous attack vectors.

## Why this is different from scenario 12

Scenario 12 checks that **one** endpoint fires its rate limiter under legitimate
listener load. This scenario adds attack sources **beyond the listener**:

| Thread | Source | Source IP | LookupSeq | Targets |
|---|---|---|---|---|
| Legitimate NACK escalation | subtx-gen → listener gap detection | fd20::21-23 | real chain hashes | retry1→retry2→retry3 (beacon-ordered chain) |
| **Rogue node flood** | source VM Python3 script | fd20::10 (not a listener) | fixed constant | all 3 simultaneously |
| **Compromised listener flood** | listener1 VM Python3 script | fd20::21 (legitimate IP, abused) | random uint64 per packet | all 3 simultaneously |

## Topology

```
source -- mc --> proxy -- mc --> listener1..3 --- NACK --> retry1 (T0/P128)
                                  |                   --> retry2 (T0/P64)
                                  |                   --> retry3 (T1/P128)
                                  |
                                  +-- compromised flood --> retry1/2/3
source VM (fd20::10) -- rogue flood -------------------> retry1/2/3
```

## Escalation chain (legitimate path)

A silent rate-limit drop causes the listener to wait `respTimeout` (300ms)
then apply exponential backoff before trying the next endpoint:

```
listener → retry1 [RL drop → 300ms timeout + 500ms backoff]
         → retry2 [RL drop → 300ms timeout + 1000ms backoff]
         → retry3 [RL drop → 300ms timeout + 2000ms backoff]
         → cycle until MaxRetries=5 / GapTTL=10m
```

The rogue and compromised floods target all three endpoints **directly**,
bypassing the escalation chain.

## Tight RL injection

All three endpoints are temporarily restarted with tight RL via a systemd
drop-in (`/etc/systemd/system/retry-endpoint.service.d/rl-test.conf`):

| Var | Default scenario value | Production default |
|---|---|---|
| `RL_IP_RATE` | 5 tokens/s | 100,000 |
| `RL_IP_BURST` | 3 | 10 |
| `RL_SEQUENCE_MAX` | 2 per window | 1,000,000 |
| `RL_SEQUENCE_WINDOW` | 10s | 10s |

The EXIT trap removes the drop-in and restarts endpoints regardless of
pass/fail, so production RL config is always restored.

## NACK flood tool

Python3 inline (no extra dependencies). Constructs wire-valid 64-byte NACK
datagrams (magic `0xE3E1F3E8`, ProtoVer `0x02BF`, MsgType `0x10`, HashKey,
StartSeq, EndSeq, 32-byte SubtreeID). Each script self-terminates after
`DURATION + 25` seconds.

The rogue flood uses fixed HashKey/SeqNum values. The compromised-listener
flood uses a new random `uint64` SeqNum per packet to prove the per-IP
limiter fires even when the per-sequence window is never exhausted for any
individual seq.

## Assertions

`bre_rate_limit_drops_total` carries a `level` label (`"ip"` / `"sequence"`),
so we assert the per-IP limiter specifically:

| Counter | Filter | Endpoint | Expectation |
|---|---|---|---|
| `bsl_gaps_detected_total` | — | listener agg | > 0 |
| `bsl_nacks_dispatched_total` | — | listener agg | > 0 |
| `bre_rate_limit_drops_total` | `level="ip"` | **retry1** | **> 0** (core) |
| `bre_rate_limit_drops_total` | `level="ip"` | **retry2** | **> 0** (core) |
| `bre_rate_limit_drops_total` | `level="ip"` | **retry3** | **> 0** (core) |
| `bsl_gaps_unrecovered_total` | — | listener agg | > 0 (WARN) |

## Tunables (env)

| Var | Default | Note |
|---|---|---|
| `PPS` | 500 | |
| `DURATION` | 15s | generator duration; floods run for DURATION+25s |
| `SEQ_GAP_EVERY` | 20 | |
| `SEQ_GAP_SIZE` | 2 | |
| `SEQ_GAP_DELAY` | 2s | long delay keeps frames absent from cache |
| `RL_IP_RATE` | 5 | tight per-IP token rate |
| `RL_IP_BURST` | 3 | |
| `RL_SEQUENCE_MAX` | 2 | |
| `RL_SEQUENCE_WINDOW` | 10s | |

## Prerequisites

- retry1/2/3 deployed and healthy (`/healthz` returns 200).
- `beacon_interval: 5s` on all three (set in `retry-hosts.yml`).
- Listeners configured with `retry_endpoints` listing all three (set in
  `listener-hosts.yml`).
- Python3 available on `source` and `listener1` VMs (Ubuntu 24.04 default).

## Run

```bash
bash ~/repo/multicast-test/scenarios/14-multi-endpoint-ratelimit/run.sh
```
