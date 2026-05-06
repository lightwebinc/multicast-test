# Functional Scenario Test Results

## Test Configuration

| Parameter | Value |
|------------------|---------------------------------------------------------------------|
| Date | 2026-04-21 |
| Proxy address | `[fd20::2]:9000` |
| Proxy metrics | `http://10.10.10.20:9100` |
| Shard bits | 2 (4 groups: ff05::, ff05::1, ff05::2, ff05::3) |
| MC scope | site (ff05::/16) |
| Generator | subtx-gen, v2 frames, 1000 pps, 10 s, 8 subtrees, seed=lax-lab-2026 |
| Listener workers | 1 per listener (SO_REUSEPORT with multicast requires 1 worker) |

## Lab Topology

```
source (fd20::10)
  └─► proxy (fd20::2)  [bitcoin-shard-proxy, shard_bits=2 → 4 groups]
        └─► lxdbr1 multicast fabric (MLD snooping + querier enabled)
              ├─► listener1 (ff05::, ff05::1, ff05::2, ff05::3)  all shards, all subtrees
              ├─► listener2 (ff05::, ff05::1)                     shards 0+1, subtree_exclude=[idx 2]
              └─► listener3 (ff05::, ff05::1, ff05::2, ff05::3)  all shards, subtree_include=[idx 5]
```

## Software Versions

| Component | Version / Commit |
|------------------------|------------------------------------------------------------------------------------------|
| bitcoin-shard-proxy | local build, `github.com/lightwebinc/bitcoin-shard-proxy` (feat/v2-frame-sequencing tip) |
| bitcoin-shard-listener | local build, `github.com/lightwebinc/bitcoin-shard-listener` main tip |
| bitcoin-shard-common | v0.1.0 (92-byte BRC-124/v2 header with PrevSeq/CurSeq) |
| subtx-gen | 0225f35 |

## Scenario 01 — Functional: All Shards

**What is tested:** All 4 multicast groups delivered. listener1 (no filter) forwards 100%,
listener2 (shard 0+1, subtree_exclude) forwards ~43.75%, listener3 (subtree_include 1/8) forwards ~12.5%.

| Assertion | Got | Expected | Tolerance | Result |
|---------------------------------------|------|----------|-----------|----------|
| listener1 forwarded | 9177 | ~9177 | 5% | **PASS** |
| listener2 forwarded (shard×subtree) | 4006 | ~4014 | 10% | **PASS** |
| listener3 forwarded (subtree-include) | 1162 | ~1147 | 15% | **PASS** |

**Scenario 01: PASS** (9177 frames sent, ~920 pps)

## Scenario 02 — Functional: Shard Filter

**What is tested:** listener2 subscribes to shards 0+1 only. MLD snooping delivers only
~50% of total frames to listener2. Of those, ~1/8 are dropped by subtree_exclude. The
network-level shard filter (via multicast group join) is verified via received count.

| Assertion | Got | Expected | Tolerance | Result |
|-------------------------------------|------|----------|-----------|----------|
| listener2 received (shard 0+1 only) | 4526 | ~4540 | 5% | **PASS** |
| listener2 dropped subtree_exclude | 541 | ~567 | 20% | **PASS** |
| listener2 forwarded | 3985 | ~3972 | 10% | **PASS** |

**Scenario 02: PASS** (9080 frames sent, ~908 pps)

> **Note:** With MLD snooping enabled, the bridge delivers only the subscribed multicast
> groups to each listener. Shard filtering is therefore enforced at the network layer
> (listener2 never receives groups 2 and 3), so `bsl_frames_dropped_total{reason="shard_filter"}`
> remains 0. The received count is used instead to verify correct network-level filtering.

## Scenario 03 — Functional: Subtree Filter

**What is tested:** listener3 subscribes to all shards but includes only 1 of 8 subtrees.
~7/8 of all frames are dropped with `subtree_include_miss`, ~1/8 are forwarded.

| Assertion | Got | Expected | Tolerance | Result |
|---------------------------------------------|------|----------|-----------|----------|
| listener3 forwarded (subtree-include match) | 1074 | ~1127 | 15% | **PASS** |
| listener3 dropped subtree_include_miss | 7947 | ~7893 | 5% | **PASS** |

**Scenario 03: PASS** (9021 frames sent, ~902 pps)

## Summary

| Scenario | Result | Frames | Rate |
|--------------------------------|----------|--------|----------|
| 01 — functional-all-shards | **PASS** | 9177 | ~920 pps |
| 02 — functional-shard-filter | **PASS** | 9080 | ~908 pps |
| 03 — functional-subtree-filter | **PASS** | 9021 | ~902 pps |

All functional scenarios pass. End-to-end path verified:
subtx-gen → proxy (V2 decode + multicast egress) → lxdbr1 (MLD snooping) → listeners (filter + forward).

## Issues Fixed in This Run

1. **Proxy stale binary** — ansible `creates:` guard blocked rebuild on redeploy. The old
   binary (pre-stabilization V2 header, `jefflightweb` namespace) was incompatible with
   subtx-gen's current BRC-124 V2 frames, causing `decode_error` on every packet. Fix: removed
   `creates:` from the build task in both `bitcoin-ingress` and `bitcoin-listener` ansible
   roles; rebuilt proxy from `github.com/lightwebinc/bitcoin-shard-proxy` (uses
   `bitcoin-shard-common v0.1.0`, 92-byte BRC-124 header).

2. **Listener stale binary** — same `creates:` issue; old binary had a non-blocking DIAG polling
   loop that returned EAGAIN continuously and processed almost no frames. Fix: same `creates:`
   removal; rebuilt from `github.com/lightwebinc/bitcoin-shard-listener` main tip (blocking
   `unix.Recvfrom`).

3. **Listener multicast SO_REUSEPORT duplication** — Linux delivers multicast datagrams to ALL
   sockets in a SO_REUSEPORT group (not load-balanced like unicast). With `num_workers=2` each
   frame was processed twice, doubling all metrics. Fix: `num_workers=1` per listener
   (host-level var in inventory to override `group_vars/all.yml`).

4. **Filter drop reasons** — `filter.Allow()` returned `bool` with a single `"filtered"` reason.
   Scenario 02 and 03 expected specific labels (`shard_filter`, `subtree_exclude`,
   `subtree_include_miss`). Fix: changed `Allow()` to return `(bool, string)` with the specific
   reason; updated tests and listener call site accordingly.

5. **Scenario 02 shard_filter assertion** — test expected app-level `shard_filter` drops but
   MLD snooping enforces shard filtering at the bridge before frames reach the listener.
   Fix: replaced the shard_filter assertion with `bsl_frames_received_total` check (~50% of
   total frames received by listener2).
