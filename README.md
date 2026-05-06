# bitcoin-multicast-test

LXD-based end-to-end test lab for the Bitcoin sharding pipeline. The lab
validates
[`bitcoin-shard-proxy`](https://github.com/lightwebinc/bitcoin-shard-proxy),
[`bitcoin-shard-listener`](https://github.com/lightwebinc/bitcoin-shard-listener),
and [`bitcoin-retry-endpoint`](https://github.com/lightwebinc/bitcoin-retry-endpoint)
working together over an IPv6 multicast fabric, using
[`bitcoin-subtx-generator`](https://github.com/lightwebinc/bitcoin-subtx-generator)
as the traffic source.

```
 source ──► proxy (ingress) ──► ff05::%enp6s0 ──► listener1 / listener2 / listener3
                                      │                     │  NACK (escalating)    sink :9100
                                      ▼                     │  ① retry1 (T0/P128) → MISS
                                    retry1                  │  ② retry2 (T0/P64)  → MISS
                                    retry2                  │  ③ retry3 (T1/P128) → ACK
                                    retry3 ◄────────────────┘
                                      └──► ff05::%enp6s0 (retransmit → listeners)
```

Tests target **1000 pps / 10 s** (functional, ~10 000 frames). The LXD
bridge has a known PPS ceiling so higher rates live as historical perf
baselines under [`testing/`](testing/).

## Quickstart

```bash
git clone https://github.com/lightwebinc/bitcoin-multicast-test.git
cd bitcoin-multicast-test
chmod +x deploy.sh
bash deploy.sh           # provisions everything from scratch
```

## Layout

| Path | Purpose |
|---------------------------|------------------------------------------------------------------------------|
| `deploy.sh` | Top-level: full lab bring-up |
| `lab/01-*..09-*` | LXD provisioning + verification scripts |
| `lab/06-netplan/` | Per-VM static IP netplans |
| `ansible/` | Inventory + thin wrapper for upstream proxy/listener playbooks |
| `scenarios/` | End-to-end test scenarios (see [`scenarios/README.md`](scenarios/README.md)) |
| `docs/prometheus/` | `prometheus.yml` (source of truth for metrics VM) |
| `docs/grafana/` | Proxy + listener dashboard JSON |
| `docs/` | Network, listener/proxy, and troubleshooting docs |
| `testing/` | Historical perf baselines (pre-listener reorg) |

## VMs

| VM | mgmt (enp5s0) | egress (enp6s0) | Role |
|-------------|---------------|-----------------|-------------------------------------------------|
| `source`    | 10.10.10.10   | fd20::10/64     | runs `subtx-gen` to emit BRC-124/v2 frames      |
| `proxy` | 10.10.10.20 | fd20::2/64 | `bitcoin-shard-proxy` ingress |
| `listener1` | 10.10.10.31 | fd20::21/64 | all shards, all subtrees |
| `listener2` | 10.10.10.32 | fd20::22/64 | shards 0,1 + subtree_exclude |
| `listener3` | 10.10.10.33 | fd20::23/64 | all shards + single subtree_include |
| `retry1`    | 10.10.10.34 | fd20::24/64 | `bitcoin-retry-endpoint` Tier 0 / Pref 128 (primary) |
| `retry2`    | 10.10.10.35 | fd20::25/64 | `bitcoin-retry-endpoint` Tier 0 / Pref 64 (secondary) |
| `retry3`    | 10.10.10.36 | fd20::26/64 | `bitcoin-retry-endpoint` Tier 1 / Pref 128 (escalation target) |
| `metrics` | 10.10.10.142 | — | Prometheus :9090 + Grafana :3000 (pre-existing) |

## Documentation

- [`docs/network.md`](docs/network.md) — bridge layout, VM IPs, multicast groups
- [`docs/bitcoin-shard-proxy.md`](docs/bitcoin-shard-proxy.md) — proxy deploy notes
- [`docs/bitcoin-shard-listener.md`](docs/bitcoin-shard-listener.md) — listener deploy notes
- [`docs/retransmission-testing.md`](docs/retransmission-testing.md) — retry-endpoint deploy + NACK testing notes
- [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md) — common issues
- [`scenarios/README.md`](scenarios/README.md) — test scenario index

## Metrics

The `metrics` VM hosts Prometheus and Grafana. After any topology change
run:

```bash
bash lab/09-metrics-update.sh
```

This pushes the canonical `docs/prometheus/prometheus.yml` and imports
both dashboards under `docs/grafana/` via the Grafana HTTP API.
