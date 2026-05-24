# LXD VM Lab

Linux LXD-based end-to-end test lab for the Bitcoin sharding pipeline. This
provisions persistent Ubuntu VMs on a host machine and runs bash scenario
scripts against them over an IPv6 multicast fabric (`fd20::/64`).

> **Note:** The primary test framework is the Go Docker harness in `harness/`.
> See the [top-level README](../README.md) for quickstart.

## Quickstart

```bash
cd vm-lab
chmod +x deploy.sh
bash deploy.sh           # provisions everything from scratch
bash scenarios/run-all.sh
```

## Layout

| Path               | Purpose                                                                             |
| ------------------ | ----------------------------------------------------------------------------------- |
| `deploy.sh`        | Full lab bring-up (LXD VMs + Ansible + verification)                                |
| `lab/01-*..09-*`   | LXD provisioning + verification scripts                                             |
| `lab/06-netplan/`  | Per-VM static IP netplans                                                           |
| `ansible/`         | Inventory + thin wrapper for upstream proxy/listener playbooks                      |
| `scenarios/`       | Bash end-to-end test scenarios (see [`scenarios/README.md`](scenarios/README.md))   |
| `docs/prometheus/` | `prometheus.yml` (source of truth for metrics VM)                                   |
| `docs/grafana/`    | Proxy + listener dashboard JSON                                                     |
| `docs/`            | Network, listener/proxy, and troubleshooting docs                                   |

## VMs

| VM          | mgmt (enp5s0) | egress (enp6s0) | Role                                                                      |
| ----------- | ------------- | --------------- | ------------------------------------------------------------------------- |
| `source`    | 10.10.10.10   | fd20::10/64     | runs `subtx-gen` to emit BRC-124 frames                                   |
| `proxy`     | 10.10.10.20   | fd20::2/64      | `bitcoin-shard-proxy` ingress                                             |
| `listener1` | 10.10.10.31   | fd20::21/64     | all shards, all subtrees; mc-egress re-emits ff05→ff02                    |
| `listener2` | 10.10.10.32   | fd20::22/64     | shards 0,1 + subtree_exclude                                              |
| `listener3` | 10.10.10.33   | fd20::23/64     | all shards + single subtree_include                                       |
| `listener4` | 10.10.10.37   | fd20::27/64     | `ff02::` subscriber; terminal consumer for mc-egress bridge (scenario 05) |
| `retry1`    | 10.10.10.34   | fd20::24/64     | `bitcoin-retry-endpoint` Tier 0 / Pref 128 (primary)                      |
| `retry2`    | 10.10.10.35   | fd20::25/64     | `bitcoin-retry-endpoint` Tier 0 / Pref 64 (secondary)                     |
| `retry3`    | 10.10.10.36   | fd20::26/64     | `bitcoin-retry-endpoint` Tier 1 / Pref 128 (escalation target)            |
| `metrics`   | 10.10.10.142  | —               | Prometheus :9090 + Grafana :3000 (pre-existing)                           |

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
