# Scenario 21 — Subtree Group Membership Ramp

**Run time**: ~15 minutes (excluded from `run-all.sh` by default).

## Purpose

Exercises the BRC-127 + BRC-124 integration over time to populate dashboard
time-series. Unlike scenario 20 (which announces all subtrees at once),
scenario 21 adds one subtree to the group every 75 seconds, producing a
visible ramp in `bsl_subtree_group_entries`, forwarding rate, and drop rate.
After the generator exits the 90-second TTL expires and `bsl_subtree_group_evictions_total`
spikes, returning the registry to empty.

## What is tested

| Assertion | Mechanism |
|---|---|
| Negative delivery | During the initial 75s (0 subtrees), all BRC-124 frames are dropped (`subtree_include_miss ≈ received`) |
| Positive delivery | During the stable phase (all 8 subtrees), all BRC-124 frames are forwarded (`forwarded ≈ received`) |
| Control plane | `bsl_subtree_announces_received_total > 0` |
| TTL eviction | `bsl_subtree_group_evictions_total > 0` after drain; live gauge returns to 0 |

## Dashboard metrics

- `bsl_subtree_group_entries` — gauge ramps 0 → 8 over ~10 min, drops to 0 after drain
- `bsl_subtree_announces_received_total` — monotonically rising with each re-announce tick
- `bsl_subtree_group_evictions_total` — spikes at end when TTL expires
- `bsl_frames_dropped_total{reason="subtree_include_miss"}` — high early, falls as subtrees join
- `bsl_frames_forwarded_total{proto="udp"}` — rises to ~100% during stable phase

## Prerequisites

- Proxy: `TCP_LISTEN_PORT=9002` in `/etc/bitcoin-shard-proxy/config.env`
  (the script enables it inline if `SKIP_RECONFIG=0`, which is the default).
- listener3: no static `SUBTREE_INCLUDE`; `SUBTREE_GROUPS=bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf`
  (the script sets this inline and restores on exit).
- `subtx-gen` on source VM built from current source (supports `-announce-phase-size`
  and `-announce-phase-interval` flags).

## Running

```bash
bash ~/repo/bitcoin-multicast-test/scenarios/21-subtree-group-ramp/run.sh
```

Skip reconfiguration if VMs are already configured from scenario 20:

```bash
SKIP_RECONFIG=1 bash ~/repo/bitcoin-multicast-test/scenarios/21-subtree-group-ramp/run.sh
```

## Timing parameters (overrideable via env)

| Variable | Default | Notes |
|---|---|---|
| `GEN_DURATION` | `12m` | Total generator run time |
| `ANNOUNCE_PHASE_SIZE` | `1` | Subtrees added per tick |
| `ANNOUNCE_PHASE_INTERVAL` | `75s` | Seconds between additions (8×75=600s ramp) |
| `ANNOUNCE_INTERVAL` | `12s` | TTL refresh period |
| `ANNOUNCE_TTL` | `90` | Entry TTL in seconds |
| `DRAIN_WAIT` | `150` | Seconds to wait post-generator for evictions |
