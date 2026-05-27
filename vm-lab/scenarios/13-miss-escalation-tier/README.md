# Scenario 13 — MISS Escalation by Tier and Preference

Tests that the listener gap tracker escalates NACK retries through the
beacon-discovered registry in tier/preference order when earlier endpoints
respond with MISS.

## Setup

Three retry endpoints are configured with distinct tier/preference values:

| Endpoint | Tier | Preference | Role in this scenario |
|----------|------|------------|-----------------------|
| retry1   | 0    | 128        | MISS (ingress blocked, cache empty) |
| retry2   | 0    | 64         | MISS (ingress blocked, cache empty) |
| retry3   | 1    | 128        | ACK  (cache warm, retransmits frames) |

The listener registry sorts endpoints by **(Tier ASC, Preference DESC)**,
so the escalation order is: retry1 → retry2 → retry3.

## Mechanism

- **Beacon discovery**: All three retry endpoints advertise their tier and
  preference via ADVERT beacons (multicast UDP to the beacon group every 5 s).
  Listeners update their registry upon each received ADVERT.
  Static seeds (`retry_endpoints` in the service config) coexist at
  Tier=0xFF/Preference=0 and sort below beacon entries in the registry.
  With `NACK_MAX_RETRIES=8`, there is enough budget to reach the seeds as
  well if any beacon entries time out.

- **NACK escalation**: On MISS, the gap tracker calls `advanceEndpoint`,
  which increments `endpointIdx` immediately (no backoff wait). The next
  NACK is fired to the next endpoint in the sorted snapshot.

- **Gap creation**: Natural multicast delivery loss on the LXD bridge
  (at 1000 pps) creates HashKey/SeqNum chain breaks at the listeners.
  The proxy stamps HashKey/SeqNum monotonically, so subtx-gen's
  `--seq-gap-*` flags do NOT create chain gaps — all gaps come from
  delivery loss only.

## Expected outcome

```
bsl_gaps_detected_total     > 0     (natural multicast loss)
bsl_nacks_dispatched_total  > 0
bsl_gaps_unrecovered_total  == 0    (all gaps resolved by retry3)

retry1: bre_frames_cached_total == 0    (ingress blocked)
        bre_nack_requests_total  > 0    (received NACKs first)
        bre_cache_misses_total   > 0    (responded with MISS)

retry2: bre_frames_cached_total == 0    (ingress blocked)
        bre_nack_requests_total  > 0    (escalated from retry1 MISS)
        bre_cache_misses_total   > 0    (responded with MISS)

retry3: bre_frames_cached_total  > 0    (received multicast normally)
        bre_cache_hits_total     > 0    (answered NACKs from cache)
        bre_retransmits_total    > 0    (sent retransmit multicast)
```

## Prerequisites

- All three retry endpoints deployed and running (`ansible/retry-hosts.yml`).
- Listeners deployed with `retry_endpoints` listing all three endpoints
  (`ansible/listener-hosts.yml`) and beacon multicast firewall rule in place.
- `beacon_interval: 5s` set on all retry endpoints.
- `NACK_MAX_RETRIES=8` on listeners (default 5 is insufficient for a 6-entry
  registry: 3 beacon + 3 static seeds). Set in `/etc/shard-listener/config.env`.

## nack.go bug fixes (2026-05-05)

Two bugs were found and fixed in `shard-listener/nack/nack.go` while
debugging this scenario:

1. **Phantom gaps from retransmitted frames** — `Observe()` created a new gap
   entry even for out-of-order retransmits (`seqNum < lastSeqNum`), inflating
   `bsl_gaps_detected_total` by ~10x and causing cascading false unrecovered gaps.
   Fix: skip gap detection when `seqNum <= lastSeqNum`; only advance
   `lastSeqNum` forward (never regress on retransmit or duplicate).

2. **Sweep re-dispatch of in-flight gaps** — `sweepOnce()` did not stamp
   `nextAttempt` before copying the `gapEntry` to the `nackQueue`, so the same
   gap could be enqueued multiple times per sweep tick. This prematurely
   exhausted `MaxRetries` before the gap could escalate through all endpoints.
   Fix: stamp `nextAttempt = now + respTimeout + 100ms` before enqueue. If the
   queue is full, reset `nextAttempt = now` so the gap is retried next tick.
