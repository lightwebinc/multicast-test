# Scenario 12 -- Burst Gap + Rate Limiting

**Goal:** verify the retry endpoint's rate limiter activates under a burst of
NACK requests from multiple listeners, and that the system degrades gracefully
(some gaps recover, others are rate-limited) without crashing or deadlocking.

## Topology

```
source -- mc --> proxy -- mc --> listener1..3 (NACK --> retry1)
                                                       |
                                 <-- mc retransmit ----+
```

## How it works

`subtx-gen` is run at moderate PPS with frequent multi-frame gaps
(`-seq-gap-every 50 -seq-gap-size 3 -seq-gap-delay 500ms`). Each gap creates
3 consecutive missing frames, causing each listener to open multiple gap entries
simultaneously. With 3 listeners NACKing the same gaps, the retry endpoint
receives a concentrated burst of NACK traffic.

The intent is to:

1. Exercise the `SEQ_GAP_SIZE > 1` code path (multiple consecutive missing seqs).
2. Drive enough NACKs to trigger the per-IP and/or per-sequence rate limiter.
3. Verify that retransmits still occur (not everything is rate-limited).
4. Verify that `bre_rate_limit_drops_total > 0` (the rate limiter fired).

## Assertions

| Counter | Expectation |
|--------------------------------------|--------------------------------------|
| `bsl_gaps_detected_total` | > 0 |
| `bsl_nacks_dispatched_total` | > 0 |
| `bre_nack_requests_total` | > 0 |
| `bre_rate_limit_drops_total` | > 0 (core assertion) |
| `bre_retransmits_total` | > 0 (some NACKs still served) |
| `bsl_gaps_suppressed_total` | > 0 (at least some gaps recovered) |

## Tunables (env)

| Var | Default | Note |
|-----------------|---------|---------------------------------------|
| `PPS` | 500 | moderate rate |
| `DURATION` | 15s | |
| `SEQ_GAP_EVERY` | 50 | frequent gaps |
| `SEQ_GAP_SIZE` | 3 | multi-frame consecutive gaps |
| `SEQ_GAP_DELAY` | 500ms | transient -- cache will eventually have it |

## Run

```bash
bash ~/repo/bitcoin-multicast-test/scenarios/12-burst-gap-ratelimit/run.sh
```
