# multicast-test

> Part of the [**BSV Layered Multicast**](https://github.com/lightwebinc/bsv-multicast) open-source project — see the main repository for the full architecture, design docs, and BRC specifications.

End-to-end test suite for the Bitcoin multicast sharding pipeline. Validates
[`shard-proxy`](https://github.com/lightwebinc/shard-proxy),
[`shard-listener`](https://github.com/lightwebinc/shard-listener),
[`retry-endpoint`](https://github.com/lightwebinc/retry-endpoint),
[`subtx-generator`](https://github.com/lightwebinc/subtx-generator),
[`beef-generator`](https://github.com/lightwebinc/beef-generator),
and [`shard-manifest`](https://github.com/lightwebinc/shard-manifest)
working together over an IPv6 multicast fabric.

This repo is the **integration** test suite — the Go Docker harness:

| Framework | Location | Runtime | Description |
|-----------|----------|---------|-------------|
| **Go Docker harness** | `harness/` | Docker containers on `fd10::/64` | Scenario tests driven by `go test`. |

> Deployment / applied-infrastructure testing (the LXD VM lab, the privileged
> netns mesh repros, and the real-host deployment tooling) is maintained
> separately from this public integration suite.

```
 source ──► proxy (ingress) ──► multicast fabric ──► listener1 / listener2 / listener3
                                      │                 │       │  NACK (escalating)    sink
                                      ▼          mc-egress      │  ① retry1 (T0/P128) → MISS
                                    retry1              │       │  ② retry2 (T0/P64)  → MISS
                                    retry2              │       │  ③ retry3 (T1/P128) → ACK
                                    retry3 ◄────────────│───────┘
                                      └──► multicast fabric (retransmit → listeners)
                                    listener4 ◄─────────┘  (link-local subscriber, scenario 05)
```

## Quickstart — Go Docker harness

### Prerequisites

Docker, Go 1.26.2+ (the `go.mod` floor), and root (tests create network namespaces). The harness
compiles component binaries from **sibling checkouts** — clone the component
repos side by side under one parent directory:

```
<parent>/
├── multicast-test      (this repo)
├── shard-common
├── shard-proxy
├── shard-listener
├── retry-endpoint
├── subtx-generator
├── beef-generator      (BEEF scenarios 92–98)
└── shard-manifest      (scenario 73)
```

If the parent directory has a `go.work` listing the repos, the builder uses
it; otherwise it injects a temporary `replace` directive pointing at the
sibling `shard-common` checkout.

### Running

```bash
make test          # all harness scenarios (~30 min)
make test-quick    # tier-1 filter scenarios (~60s)
make test-retransmit  # NACK/retransmit scenarios
make test-subtree-announce  # BRC-127 subtree announce scenarios
make test-frag     # fragmentation scenarios
make test-block    # BRC-131/132/134/135 block / subtree / anchor / header scenarios
make test-bgp      # BGP ingress / anycast scenarios (currently all skip — deferred)
make test-dedup    # TxID dedup scenarios
make test-ssm      # SSM scenarios (RFC 4607)
make test-manifest # BRC-139 manifest / auto-shard-config scenarios
make test-coalesce # BRC-142 coalescing / bundle-frame scenarios
make test-beef     # BRC-148 BEEF object plane scenarios
make test-one T=Scenario36  # a single scenario test by name
make help          # show all targets
```

Individual scenarios:

```bash
sudo go test ./harness/scenarios/... -v -run TestScenario01
```

## Scenarios

[`SCENARIOS.md`](SCENARIOS.md) is the canonical per-scenario index (titles,
test names, make-target filters). Highlights:

- **60/61 — SSM (RFC 4607)** (`make test-ssm`): `netjoin` source-group
  join/leave sanity plus ASM-fallback startup; see the
  [SSM Support Plan](https://github.com/lightwebinc/bsv-multicast/blob/main/DESIGN.md#source-specific-multicast-ssm).
- **70–75 — BRC-139 manifest, unified logging, NACK proxying**:
  `make test-manifest` (filter `Scenario7[0-5]`) runs 70–72 (wire pipeline,
  live-reshard signal, adoption safety gates), 73 — the `shard-manifest`
  [unified logging](https://github.com/lightwebinc/shard-common/blob/main/docs/logging.md)
  emit contract (needs only `go` + loopback) — and 74–75 (cross-domain NACK
  proxying, inter-fabric RTT repair); any one runs alone via
  `make test-one T=Scenario73`.
- **92–98 — BRC-148 BEEF object plane** (`make test-beef`): submission-record
  ingress (open port + dedicated lane), topic/version filtered delivery,
  fragmentation, NACK recovery, per-domain manifest coordination, and
  concurrent-plane independence
- **89–91 — BRC-142 coalescing** (`make test-coalesce`): origin-proxy bundle
  packing, listener edge-decoalescing, and bundle-unit NACK recovery; see the
  [BRC-142 spec](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/brc-142-coalescing-frame.md).

## Layout

| Path | Purpose |
|------|---------|
| `Makefile` | `make test` targets for the Go Docker harness |
| `SCENARIOS.md` | Canonical scenario index (per-scenario descriptions) |
| `harness/scenarios/` | Go test files — one per scenario |
| `harness/build/` | Docker image builder (compiles binaries, creates minimal images) |
| `harness/driver/` | Docker driver (container lifecycle, network) |
| `harness/env/` | Network emulation (`tc netem`) and firewall (`ip6tables`) helpers |
| `harness/metrics/` | Prometheus scraper and assertion helpers |
| `vm-lab/scenarios/` | Archived before/after metrics snapshots (TSV) from the former VM-lab runs — reference data only; nothing in the harness reads them |
