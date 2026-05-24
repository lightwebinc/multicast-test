# Network topology

## Bridges

| Bridge | Subnet           | Purpose                                       |
| ------ | ---------------- | --------------------------------------------- |
| lxdbr0 | 10.10.10.0/24    | Management — SSH, LXD agent, package installs |
| lxdbr1 | fd20::/64 (IPv6) | Egress fabric — multicast traffic only        |
| lxdbr2 | 203.0.113.0/30 + 2001:db8:b::/64 | BGP p2p eBGP link (router1 ↔ router2) |
| lxdbr3 | 198.51.100.16/28 + 2001:db8:d::/64 | BGP iBGP peering LAN (router2 + proxies) |
| lxdbr4 | 198.51.100.64/28 + 2001:db8:e::/64 | Source LAN (source + router1) |

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
| source    | 10.10.10.10   | 198.51.100.66 + 2001:db8:e::10 | ubuntu-source | Traffic source (via router1→BGP→VIP) | host-only              |
| proxy     | 10.10.10.20   | fd20::2/64      | ubuntu-small-mcast | Ingress proxy (VIP: 192.0.2.1 + 2001:db8:ffff::1) | open |
| proxy2    | 10.10.10.21   | fd20::3/64      | ubuntu-small-mcast | Ingress proxy 2 (same AnyCast VIP)    | open                   |
| listener1 | 10.10.10.31   | fd20::21/64     | ubuntu-small-mcast | Listener (all, mc-egress enabled)     | `enable_firewall=true` |
| listener2 | 10.10.10.32   | fd20::22/64     | ubuntu-small-mcast | Listener (filter)                     | `enable_firewall=true` |
| listener3 | 10.10.10.33   | fd20::23/64     | ubuntu-small-mcast | Listener (subtree)                    | `enable_firewall=true` |
| listener4 | 10.10.10.37   | fd20::27/64     | ubuntu-small-mcast | Listener (mc-egress consumer, ff02::) | `enable_firewall=true` |
| retry1    | 10.10.10.34   | fd20::24/64     | ubuntu-small-mcast | Retry endpoint T0/P128                | `enable_firewall=true` |
| retry2    | 10.10.10.35   | fd20::25/64     | ubuntu-small-mcast | Retry endpoint T0/P64                 | `enable_firewall=true` |
| retry3    | 10.10.10.36   | fd20::26/64     | ubuntu-small-mcast | Retry endpoint T1/P128                | `enable_firewall=true` |
| redis     | 10.10.10.40   | —               | ubuntu-small-single | Redis dedup backend (mgmt-only)      | host-managed           |
| metrics   | 10.10.10.142  | —               | pre-existing       | Prometheus + Grafana                  | host-managed           |
| router1   | 10.10.10.51   | —               | ubuntu-bgp-r1      | BGP upstream router (AS 65000), lxdbr2+lxdbr4 | open           |
| router2   | 10.10.10.53   | —               | ubuntu-bgp-r2      | BGP PE router (AS 65001), lxdbr2+lxdbr3 | open                 |

Default gateway for all VMs: `10.10.10.1` (lxdbr0 host address).

Listener firewall allow-list:

- `mgmt_cidrs_v4: ["10.10.10.0/24"]` — SSH + metrics scrape
- `mgmt_cidrs_v6: ["fd20::/64"]`

## LXD profiles

| Profile             | NICs              | Notes                          |
| ------------------- | ----------------- | ------------------------------ |
| ubuntu-small-mcast  | eth0 + eth1       | 2 vCPU, 2 GiB RAM, 15 GiB disk |
| ubuntu-small-single | eth0 only         | Same resources; reference only |
| ubuntu-source       | eth0 + eth1       | eth1→lxdbr4 (source LAN)       |
| ubuntu-bgp-r1       | eth0 + eth1 + eth2| eth1→lxdbr2, eth2→lxdbr4       |
| ubuntu-bgp-r2       | eth0 + eth1 + eth2| eth1→lxdbr2, eth2→lxdbr3       |

`eth0` attaches to `lxdbr0`; `eth1` attaches to `lxdbr1` (for mcast profiles)
or the profile-specific bridge. Inside Ubuntu 24.04 VMs these appear as
`enp5s0`, `enp6s0`, and `enp7s0` respectively.

Proxies and routers also have `eth2` attached via device overrides or profiles
(e.g., proxy's `eth2→lxdbr3` for iBGP peering).

## Topology diagram

```
       [mgmt bridge: lxdbr0 — 10.10.10.0/24 (host .1)]
          |     |     |      |     |     |     |     |     |    |    |
        source proxy proxy2  l1    l2    l3    r1    r2    r3  redis metrics
          |                  |     |     |           |     |
       [lxdbr4]           [egress: lxdbr1 — fd20::/64, multicast snooping]
          |                  |     |     |           |
        router1 ──eBGP──► router2 ──iBGP──► proxy  proxy2
       [lxdbr2: p2p]     [lxdbr3: peering]  (VIP: 2001:db8:ffff::1)

 source ►► [2001:db8:ffff::1]:9000 (VIP via BGP)
           router1 → router2 → proxy/proxy2 (ECMP)
              proxy ►► ff05::%enp6s0 ►► listener1/2/3 ►► 127.0.0.1:9100 sink
             proxy2 ►► ff05::%enp6s0 ►► listener1/2/3 (same multicast groups)
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
across four groups `ff05::b:0..3`. Listeners join groups according to
their `shard_include` filter — listener1 and listener3 join all four,
listener2 joins only `ff05::b:0` and `ff05::b:1`.

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
pushed to `/etc/netplan/99-lab.yaml` by `lab/06-netplan.sh`. Proxy BGP
peering overlays (`proxy-bgp.yaml`, `proxy2-bgp.yaml`) are pushed as
`/etc/netplan/98-bgp.yaml`.

The `source` VM connects to `lxdbr4` (source LAN) and reaches the proxy
via the BGP path: `source → router1 → router2 → proxy/proxy2` using the
AnyCast VIP `[2001:db8:ffff::1]:9000`.
