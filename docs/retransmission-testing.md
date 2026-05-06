# Retransmission Testing Guide

This document describes end-to-end testing scenarios for the BRC-TBD-retransmission
retransmission protocol, including NACK dispatch, ACK/MISS response handling,
beacon discovery, and tier-based escalation.

## Overview

The retransmission pipeline involves three components:

1. **Listeners** — detect sequence gaps and send NACKs to retry endpoints
2. **Retry endpoints** — receive NACKs, look up cached frames, respond with ACK/MISS
3. **Beacon discovery** — retry endpoints multicast ADVERT beacons; listeners
   maintain a dynamic endpoint registry

Testing validates:

- Gap detection and NACK generation
- ACK response → gap cancellation
- MISS response → immediate escalation to next endpoint
- Tier-based endpoint ordering and preference weighting
- Beacon discovery upsert/eviction
- Flood prevention (dedup, rate limiting, semaphore bounds)

## Prerequisites

- LXD lab environment with proxy, listener, and retry-endpoint VMs
- All binaries built from current source (see `deploy.sh`)
- Redis running on retry-endpoint VMs (if using redis cache backend)

## Scenario 1: Single-Endpoint ACK

**Goal:** Verify that a listener detects a gap, sends a NACK, receives an ACK,
and cancels the gap.

### Setup

1. Start one retry endpoint (tier 0, pref 128) with beacon enabled
2. Start one listener pointed at the retry endpoint (via seed or beacon)
3. Inject a stream of frames with a deliberate gap (skip seq 5)

### Expected Behaviour

- Listener detects gap at seq 5
- Listener sends NACK to retry endpoint
- Retry endpoint finds frame in cache, retransmits, responds with ACK
- Listener cancels gap entry
- Metric: `bsl_nacks_dispatched_total` increments by 1
- Metric: `bsl_gaps_suppressed_total` increments by 1

### Verification

```bash
# Check listener metrics
curl -s http://listener1:9200/metrics | grep bsl_nacks_dispatched
curl -s http://listener1:9200/metrics | grep bsl_gaps_suppressed
```

## Scenario 2: Single-Endpoint MISS + Escalation

**Goal:** Verify that on cache MISS, the listener immediately advances to the
next endpoint without backoff.

### Setup

1. Start two retry endpoints:
   - EP1: tier 0, pref 200 (empty cache)
   - EP2: tier 0, pref 100 (has the missing frame)
2. Listener discovers both via beacon
3. Inject frames with a gap

### Expected Behaviour

- Listener sends NACK to EP1 (highest preference at tier 0)
- EP1 responds with MISS
- Listener immediately sends NACK to EP2 (no backoff on MISS)
- EP2 responds with ACK
- Gap cancelled

### Verification

```bash
curl -s http://listener1:9200/metrics | grep bsl_nacks_dispatched
# Should show 2 NACKs dispatched
```

## Scenario 3: Cross-Tier Escalation

**Goal:** Verify tier escalation when all tier-0 endpoints return MISS.

### Setup

1. Start three retry endpoints:
   - EP1: tier 0, pref 128 (empty cache)
   - EP2: tier 0, pref 64 (empty cache)
   - EP3: tier 1, pref 128 (has the missing frame)
2. Listener discovers all three via beacon

### Expected Behaviour

- NACK → EP1 → MISS → NACK → EP2 → MISS → NACK → EP3 → ACK
- Total NACKs: 3
- No backoff between MISS responses

## Scenario 4: Beacon Discovery

**Goal:** Verify that listeners dynamically discover retry endpoints via
ADVERT beacons and that the registry sorts by (Tier ASC, Preference DESC).

### Setup

1. Start listener with no seed endpoints
2. Start a retry endpoint with beacon enabled (tier 0, pref 128)
3. Wait for at least one beacon interval (default 60s; use shorter for testing)

### Expected Behaviour

- Listener receives ADVERT on beacon group
- Listener upserts endpoint into registry
- Subsequent NACKs are sent to the discovered endpoint

### Verification

```bash
# Check listener logs for discovery upsert
journalctl -u bitcoin-shard-listener | grep "upserted endpoint"
```

## Scenario 5: Beacon Eviction

**Goal:** Verify that endpoints are evicted from the registry after
3 × BeaconInterval without a refresh.

### Setup

1. Start a retry endpoint with a short beacon interval (5s)
2. Wait for listener to discover it
3. Stop the retry endpoint
4. Wait 15+ seconds (3 × 5s)

### Expected Behaviour

- Endpoint is evicted from the listener's registry
- Subsequent NACKs fall back to seed endpoints (if configured)

## Scenario 6: Draining Flag

**Goal:** Verify that ADVERT with FlagDraining is ignored by listeners.

### Setup

1. Start a retry endpoint with `beacon_flags_draining: true`
2. Verify that the listener does NOT add it to the registry

## Scenario 7: Response Suppression

**Goal:** Verify `suppress_ack` and `suppress_miss` options.

### Setup

1. Start a retry endpoint with `suppress_ack: true`
2. Send a NACK for a cached frame
3. Verify no ACK response is received (listener falls back to timeout + backoff)

## Running Scenarios

Each scenario can be run manually using the LXD lab, or automated via scripts
in `scenarios/`. Existing automated scenarios follow the convention
`NN-name/run.sh`.

Current state of the seven scenarios above:

| #   | Scenario              | Automated script                              | Status      | Blockers   |
| --- | --------------------- | --------------------------------------------- | ----------- | ---------- |
| 1   | Single-endpoint ACK   | `scenarios/10-single-endpoint-ack/`           | Implemented | —          |
| 2   | MISS + escalation     | `scenarios/13-miss-escalation-tier/` (see §3) | Implemented | —          |
| 3   | Cross-tier escalation | `scenarios/13-miss-escalation-tier/`          | Implemented | —          |
| 4   | Beacon discovery      | (pending)                                     | Ready       | script TBD |
| 5   | Beacon eviction       | (pending)                                     | Ready       | script TBD |
| 6   | Draining flag         | (pending)                                     | Ready       | script TBD |
| 7   | Response suppression  | (pending)                                     | Ready       | script TBD |

Scenarios 2 and 3 are both covered by `scenarios/13-miss-escalation-tier/run.sh`,
which deploys retry1 (T0/P128), retry2 (T0/P64), and retry3 (T1/P128), blocks
ingress on retry1 and retry2, and verifies full escalation to retry3.

Scenarios 4–7 are "Ready" now that `bitcoin-retry-endpoint` wires
`-beacon-*` and `-suppress-ack` / `-suppress-miss` flags into runtime
behaviour; only the driver scripts remain.

## Metrics Reference

| Metric                       | Component      | Description                          |
| ---------------------------- | -------------- | ------------------------------------ |
| `bsl_gaps_detected_total`    | listener       | New gaps detected                    |
| `bsl_gaps_suppressed_total`  | listener       | Gaps cancelled (fill or ACK)         |
| `bsl_nacks_dispatched_total` | listener       | NACK datagrams sent                  |
| `bsl_gaps_unrecovered_total` | listener       | Gaps evicted (TTL/retries exhausted) |
| `bre_nack_requests_total`    | retry-endpoint | NACKs received                       |
| `bre_cache_hits_total`       | retry-endpoint | Cache hits                           |
| `bre_cache_misses_total`     | retry-endpoint | Cache misses                         |
| `bre_retransmits_total`      | retry-endpoint | Frames retransmitted                 |
