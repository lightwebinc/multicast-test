# multicast-test

End-to-end test suite for the Bitcoin multicast sharding pipeline. Validates
[`shard-proxy`](https://github.com/lightwebinc/shard-proxy),
[`shard-listener`](https://github.com/lightwebinc/shard-listener),
[`retry-endpoint`](https://github.com/lightwebinc/retry-endpoint),
and [`subtx-generator`](https://github.com/lightwebinc/subtx-generator)
working together over an IPv6 multicast fabric.

Two test frameworks are provided:

| Framework | Location | Runtime | Description |
|-----------|----------|---------|-------------|
| **Go Docker harness** | `harness/` | Docker containers on `fd10::/64` | Primary. 40 scenario tests driven by `go test`. |
| **LXD VM lab** | `vm-lab/` | LXD VMs on `fd20::/64` | Legacy. Bash `run.sh` scripts against persistent VMs. |

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

Requires Docker and Go 1.25+. Tests run as root (for network namespaces).

```bash
make test          # all 40 scenarios (~30 min)
make test-quick    # tier-1 filter scenarios (~60s)
make test-retransmit  # NACK/retransmit scenarios
make test-frag     # fragmentation scenarios
make help          # show all targets
```

Individual scenarios:

```bash
sudo go test ./harness/scenarios/... -v -run TestScenario01
```

### SSM (Source-Specific Multicast)

Two scenarios validate the SSM rollout (see
[bsv-multicast SSM Support Plan](https://github.com/lightwebinc/bsv-multicast/blob/main/docs/SourceSpecificMulticast/ssm-support-plan.md)):

- **Scenario 60** — `TestScenario60_SSMLoopback`: process-local
  sanity check that `shard-common/netjoin` issues
  `MCAST_JOIN_SOURCE_GROUP` (and the matching leave) on lo. Also
  exercises `shard.Prefix(SSM, site)` → `FF35`. Does not require
  Docker.
- **Scenario 61** — `TestScenario61_SSMASMFallback`: starts a proxy +
  listener with `SOURCE_MODE=asm` to verify the SSM scaffolding is
  no-op when disabled (the new env vars must be accepted without
  changing ASM behavior).

Full Posture C cross-container delivery requires PIM-SSM in the
inter-container fabric, which Docker's default bridge does not
provide; that is validated on real fabric hosts (no vm-lab variants).

```bash
# Run both SSM scenarios
make test-ssm

# Just the loopback test — fast, no Docker required
go test ./harness/scenarios/ -v -run TestScenario60_SSMLoopback
```

## Layout

| Path | Purpose |
|------|---------|
| `Makefile` | `make test` targets for the Go Docker harness |
| `harness/scenarios/` | Go test files — one per scenario |
| `harness/build/` | Docker image builder (compiles binaries, creates minimal images) |
| `harness/driver/` | Docker driver (container lifecycle, network) |
| `harness/env/` | Network emulation (`tc netem`) and firewall (`ip6tables`) helpers |
| `harness/metrics/` | Prometheus scraper and assertion helpers |
| `vm-lab/` | LXD VM lab — see [`vm-lab/README.md`](vm-lab/README.md) |

## LXD VM lab

The legacy test lab provisions persistent LXD VMs and runs bash scenario
scripts against them. See [`vm-lab/README.md`](vm-lab/README.md) for the
VM quickstart, topology, and scenario index.
