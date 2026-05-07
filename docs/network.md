# Network topology

## Bridges

| Bridge | Subnet           | Purpose                                       |
| ------ | ---------------- | --------------------------------------------- |
| lxdbr0 | 10.10.10.0/24    | Management — SSH, LXD agent, package installs |
| lxdbr1 | fd20::/64 (IPv6) | Egress fabric — multicast traffic only        |

Multicast snooping is enabled on `lxdbr1` via sysfs:

```
/sys/devices/virtual/net/lxdbr1/bridge/multicast_snooping = 1
/sys/devices/virtual/net/lxdbr1/bridge/multicast_querier   = 1
```

**Both settings are required.** Snooping alone is insufficient — without
a querier the bridge never sends MLD queries, so listener ports appear
silent and the bridge floods all multicast to all ports. The querier is
persisted by `lxd-bridge-mcast-querier.service` (installed on the LXD
host by `deploy.sh`).

## VM assignments

All VMs use Ubuntu 24.04 predictable interface names: `enp5s0` (mgmt,
lxdbr0) and `enp6s0` (egress, lxdbr1).

| VM        | enp5s0 (mgmt) | enp6s0 (egress) | LXD profile        | Role                                  | Firewall               |
| --------- | ------------- | --------------- | ------------------ | ------------------------------------- | ---------------------- |
| source    | 10.10.10.10   | fd20::10/64     | ubuntu-small-mcast | Traffic source                        | host-only              |
| proxy     | 10.10.10.20   | fd20::2/64      | ubuntu-small-mcast | Ingress proxy                         | open                   |
| listener1 | 10.10.10.31   | fd20::21/64     | ubuntu-small-mcast | Listener (all, mc-egress enabled)     | `enable_firewall=true` |
| listener2 | 10.10.10.32   | fd20::22/64     | ubuntu-small-mcast | Listener (filter)                     | `enable_firewall=true` |
| listener3 | 10.10.10.33   | fd20::23/64     | ubuntu-small-mcast | Listener (subtree)                    | `enable_firewall=true` |
| listener4 | 10.10.10.37   | fd20::27/64     | ubuntu-small-mcast | Listener (mc-egress consumer, ff02::) | `enable_firewall=true` |
| retry1    | 10.10.10.34   | fd20::24/64     | ubuntu-small-mcast | Retry endpoint T0/P128                | `enable_firewall=true` |
| retry2    | 10.10.10.35   | fd20::25/64     | ubuntu-small-mcast | Retry endpoint T0/P64                 | `enable_firewall=true` |
| retry3    | 10.10.10.36   | fd20::26/64     | ubuntu-small-mcast | Retry endpoint T1/P128                | `enable_firewall=true` |
| redis     | 10.10.10.40   | —               | ubuntu-small-single | Redis dedup backend (mgmt-only)      | host-managed           |
| metrics   | 10.10.10.142  | —               | pre-existing       | Prometheus + Grafana                  | host-managed           |

Default gateway for all VMs: `10.10.10.1` (lxdbr0 host address).

Listener firewall allow-list:

- `mgmt_cidrs_v4: ["10.10.10.0/24"]` — SSH + metrics scrape
- `mgmt_cidrs_v6: ["fd20::/64"]`

## LXD profiles

| Profile             | NICs        | Notes                          |
| ------------------- | ----------- | ------------------------------ |
| ubuntu-small-mcast  | eth0 + eth1 | 2 vCPU, 2 GiB RAM, 15 GiB disk |
| ubuntu-small-single | eth0 only   | Same resources; reference only |

`eth0` attaches to `lxdbr0`; `eth1` attaches to `lxdbr1`. Inside Ubuntu
24.04 VMs these appear as `enp5s0` and `enp6s0` respectively.

## Topology diagram

```
       [mgmt bridge: lxdbr0 — 10.10.10.0/24 (host .1)]
          |     |     |     |     |     |     |     |     |    |
        source proxy l1    l2    l3    r1    r2    r3  metrics redis
          |     |     |     |     |     |     |     |
       [egress bridge: lxdbr1 — fd20::/64, IPv6 only, multicast snooping on]

 source ►► proxy ►► ff05::%enp6s0 ►► listener1/2/3 ►► 127.0.0.1:9100 sink
                             │                  │  NACK (escalating)
                             │                  │  ① retry1 (T0/P128) → MISS
                             ▼                  │  ② retry2 (T0/P64)  → MISS
                           retry1 ◄─────────────┘  ③ retry3 (T1/P128) → ACK
                           retry2 ──SET NX──► redis:6379 (10.10.10.40, mgmt-only)
                           retry3              shared dedup across retry1/2/3
                             └──► ff05::%enp6s0  (retransmit → listeners)
```

## Multicast groups

Scope: site-local (`ff05::/16`). With `shard_bits=2` the proxy fans out
across four groups `ff05::0..3`. Listeners join groups according to
their `shard_include` filter — listener1 and listener3 join all four,
listener2 joins only `ff05::0` and `ff05::1`.

**Egress domain (scenario 05):** listener1 re-emits received frames onto
`ff02::0..3` (link-local scope) via `-mc-egress-scope=link`. listener4 joins
`ff02::0..3` as the terminal downstream consumer. The same shard index and
port (9001) are used; only the scope prefix changes (`ff05` → `ff02`).

The join is performed by `bitcoin-shard-listener` itself (socket
`IPV6_JOIN_GROUP` on `enp6s0`); no separate `mcast-join.service` is
required. This replaces the pre-listener `recv1..3-mcast-join.service`
units which were retired by `lab/99-teardown-recv.sh`.

### Bridge MDB volatility

The bridge multicast database (MDB) is populated by MLD membership
reports and is **not persisted** across service restarts. After any
reboot or listener restart, MLD membership is re-emitted by
`bitcoin-shard-listener.service` — no manual intervention required:

```bash
bridge mdb show dev lxdbr1
```

The `multicast_querier` sysfs setting is also cleared on reboot.
`lxd-bridge-mcast-querier.service` restores it automatically when
`lxdbr1` comes up. Verify with:

```bash
cat /sys/devices/virtual/net/lxdbr1/bridge/multicast_querier  # expect: 1
systemctl is-active lxd-bridge-mcast-querier.service           # expect: active
```

## Netplan configs

Per-VM static IP configs live in `lab/06-netplan/<vm>.yaml` and are
pushed to `/etc/netplan/99-lab.yaml` by `lab/06-netplan.sh`. The
`source` VM has both `enp5s0` (IPv4 mgmt) and `enp6s0` (IPv6 egress)
configured so it can send frames directly to the proxy over the egress
fabric.
