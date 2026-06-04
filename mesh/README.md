# Multicast mesh testing

Phase 0 proof for the meshed-node roadmap: IPv6 multicast replication across a
mesh of point-to-point `ip6gre` tunnels, the transport the
[integrated-infra `mc-router` role](https://github.com/lightwebinc/integrated-infra/blob/main/docs/mesh.md)
configures.

## `ip6gre-mesh.sh` — privileged netns repro

Builds N node network namespaces in a full mesh of `ip6gre` tunnels, installs
the **same** smcroute static `(iif,G)->oifs` rules the Ansible role generates
(`roles/mc-router/templates/smcroute.conf.j2`), and tests multicast replication
in every direction.

```sh
sudo ./ip6gre-mesh.sh 3      # 3-node full mesh
```

Requires root, the `ip6_gre` kernel module, `smcroute >= 2.5`, and `python3`.

Per-node interfaces mirror the role exactly:

| Interface | Role                                                        |
| --------- | ----------------------------------------------------------- |
| `mc-local`| node-local multicast segment (dummy); carries the ff0X route |
| `gre6-<j>`| one `ip6gre` peer tunnel per neighbour (fabric link)        |
| `wan<i>`  | outer transport (lab bridge stands in for the internet NIC) |

### What it proves (verified)

Full-duplex multicast across a 3-node full mesh, with the kernel MFC tables
confirming `Iif: mc-local → Oifs: <tunnels>` fan-out on the source and
`Iif: <tunnel> → Oifs: mc-local` fan-in on every peer. Key findings:

- **No veth, no interface-model change.** Linux submits locally-originated
  multicast to the MFC using the **transmit** interface as the input VIF, so
  emitting on `mc-local` matches the `from mc-local to <tunnels>` rule directly;
  the co-located receiver gets its copy via `IPV6_MULTICAST_LOOP`. This is the
  collapsed-node model (proxy + listener share `mc_iface`).
- **MULTICAST flag** must be set on the ip6gre tunnels + local segment (they come
  up `POINTOPOINT,NOARP`), and smcroute needs explicit **`phyint … enable`**.
- The emit **source must be global/ULA** (link-local is never forwarded off-link).
- The outer transport must **not** be a Docker-managed bridge (`br_netfilter`
  routes bridged frames through the ip6tables FORWARD chain, which Docker sets
  to DROP) — the repro uses direct per-pair veth links. Harness concern only.

The diagnostics (`L1`–`L5` + the MFC dump) localize any failure by layer.

### Consumer leaf full-duplex

The script also attaches a **miner namespace per node** over an ip6gre consumer
tunnel (node side = `gre6-c1`, an `mc_egress` leaf; miner side = `gre6-up`) and
verifies both directions:

- **DOWN** — node1 emits; every miner (on every node) receives by joining the
  shard group over its tunnel. The mc-router fans the groups onto the consumer
  leaf on both the local-emit and fan-in paths, so a miner sees transactions
  ingested at any node.
- **UP** — miner1 sends txns by unicast to node1's proxy ingress over its tunnel.

There is no rule accepting multicast *from* a consumer leaf — consumers are
leaves that send by unicast and receive by multicast.

**FRR `pim6d`** remains the path for **partial meshes**: transit relay
(tunnel→tunnel) needs PIM RPF, which smcroute does not provide.

## Scenario 80 (Go harness)

`harness/scenarios/scenario80_test.go` drives this repro and is skipped unless
`MESH_REPRO=1` (keeps unit CI unprivileged):

```sh
sudo MESH_REPRO=1 go test ./harness/scenarios/ -run TestScenario80 -v
```

The full Docker-driver collapsed-mesh topology (3 nodes × proxy+listener+retry
sharing a netns, ip6gre + smcrouted between them, one consumer per node — the
success-demo scenario) is the next harness build on top of this proof.
