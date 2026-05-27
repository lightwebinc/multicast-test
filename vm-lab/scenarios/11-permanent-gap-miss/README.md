# Scenario 11 -- Cache-Empty MISS / Unrecovered Gaps

**Goal:** verify that when the retry endpoint's cache is empty, NACKs result in
MISS responses, and gaps are eventually evicted as unrecovered after MaxRetries.

This is the negative counterpart to scenarios 10 and 99, which both have a
populated cache that *can* recover gaps.

## Topology

```
source -- mc --> proxy -- mc --> listener1..3 (NACK --> retry1)
                           |                             |
                           +--x--> retry1 (ingress blocked, cache empty)
                                   MISS <-- NACK -------+
```

## How it works

The test restarts the retry endpoint to flush its in-memory cache, then blocks
multicast ingress (port 9001) via `ip6tables` so no new frames are cached.
The generator runs normally; natural multicast delivery issues (reorder/loss
on the LXD bridge) create HashKey/SeqNum gaps at the listeners. The retry
endpoint's NACK server (port 9300) is still reachable, but every lookup is a
cache miss. After MaxRetries, the gaps are evicted as unrecovered.

### Why gap injection (`-seq-gap-delay`) doesn't help here

The proxy stamps HashKey/SeqNum with its own per-(sender,group,subtree) monotonic
counter on every frame it receives. Application-level sequence gaps from
subtx-gen are overwritten -- the proxy's chain is always gapless. Actual
HashKey/SeqNum gaps are only created by multicast delivery loss between the
proxy and a listener.

## Assertions

| Counter | Expectation |
|---------------------------------|--------------------------------------|
| `bre_frames_cached_total` | == 0 (ingress was blocked) |
| `bsl_gaps_detected_total` | > 0 (natural multicast loss) |
| `bsl_nacks_dispatched_total` | > 0 |
| `bre_nack_requests_total` | > 0 |
| `bre_cache_misses_total` | > 0 (the core assertion) |
| `bre_retransmits_total` | == 0 (nothing to retransmit) |
| `bsl_gaps_unrecovered_total` | > 0 (gaps evicted after MaxRetries) |

## Tunables (env)

| Var | Default | Note |
|-----|---------|-------|
| `PPS` | 500 | higher PPS increases natural multicast loss |
| `DURATION` | 10s | |

## Run

```bash
bash ~/repo/multicast-test/scenarios/11-permanent-gap-miss/run.sh
```
