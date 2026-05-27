# BGP Lab

Demonstrates dual-stack eBGP + iBGP ingress AnyCast reachability for the
shard-proxy into a traditional IPv4/IPv6 network.

## Topology

```
  AS 65000 (upstream ISP)         AS 65001 (multicast infra)

       router1 ◄────eBGP────► router2 ◄────iBGP────► proxy
      198.51.100.1            198.51.100.3          AnyCast VIP:
      2001:db8:a::1           2001:db8:a::3          192.0.2.1
            │                      │                 2001:db8:ffff::1
     lxdbr2 │ p2p           lxdbr3 │ iBGP LAN            │
  203.0.113.1│            198.51.100.17│          lxdbr1 │ (multicast
  2001:db8:b::1│          2001:db8:d::1│                │  fabric)
            │                      │                    │
  203.0.113.2│            198.51.100.18│          fd20::2│
  2001:db8:b::2│          2001:db8:d::2│                │
            │                      │              ┌─────┼─────┐
       router2 ◄───────────► proxy ◄──────────► l1    l2    l3
```

## Address Plan

| Segment        | IPv4 subnet      | IPv6 subnet     | Purpose                |
|----------------|------------------|-----------------|------------------------|
| lxdbr2         | 203.0.113.0/30   | 2001:db8:b::/64 | eBGP p2p (r1 ↔ r2)    |
| lxdbr3         | 198.51.100.16/28 | 2001:db8:d::/64 | iBGP LAN (r2 + proxy)  |
| Loopbacks      | 198.51.100.x/32  | 2001:db8:a::x   | Router-IDs             |
| AnyCast VIP    | 192.0.2.0/24     | 2001:db8:ffff::/48 | Proxy advertisement |

### Node addressing

| Node    | Mgmt (lxdbr0)    | p2p / LAN                                  | Loopback            |
|---------|------------------|---------------------------------------------|---------------------|
| router1 | 10.10.10.51      | 203.0.113.1/30 + 2001:db8:b::1/64          | 198.51.100.1/32     |
| router2 | 10.10.10.53      | 203.0.113.2/30, 198.51.100.17/28 + v6      | 198.51.100.3/32     |
| proxy   | 10.10.10.20      | 198.51.100.18/28 + 2001:db8:d::2/64        | —                   |

## BGP Sessions

| From    | To      | Type  | AFI              | Notes                    |
|---------|---------|-------|------------------|--------------------------|
| router1 | router2 | eBGP  | IPv4 + IPv6 uni  | AS 65000 → AS 65001     |
| router2 | proxy   | iBGP  | IPv4 + IPv6 uni  | Both AS 65001            |

## Bring-up

```bash
# 1. Create bridges + profiles (idempotent)
bash lab/01-network.sh
bash lab/02-profiles.sh

# 2. Launch BGP VMs + add proxy NIC
LAUNCH_BGP=1 bash lab/03-launch.sh

# 3. Push netplan and apply
LAUNCH_BGP=1 bash lab/06-netplan.sh

# 4. Install FRR
bash lab/05b-bgp-packages.sh

# 5. Configure FRR via Ansible
cd ansible && ansible-playbook -i bgp-hosts.yml bgp-router.yml

# 6. Enable proxy BGP (via ingress ansible or manual birdc)
# TODO: document once proxy BGP vars are wired
```

## Verification

```bash
# Router2: show BGP summary
lxc exec router2 -- vtysh -c 'show bgp summary'

# Router2: IPv4 RIB
lxc exec router2 -- vtysh -c 'show bgp ipv4 unicast'

# Router1: verify AnyCast prefix arrived
lxc exec router1 -- vtysh -c 'show bgp ipv4 unicast 192.0.2.0/24'
lxc exec router1 -- vtysh -c 'show bgp ipv6 unicast 2001:db8:ffff::/48'

# Proxy: BIRD2 status
lxc exec proxy -- birdc show protocols
lxc exec proxy -- birdc 'show route export upstream4'
```

## Scenarios

- **40-bgp-ingress-announce** — proxy VIP appears in all RIBs
- **41-bgp-ingress-failover** — health-check failure triggers withdrawal
- **42-bgp-multi-proxy-anycast** — (future) ECMP with two proxies

## Future: Second Proxy

lxdbr3 is a /28 (14 usable hosts). To add a second proxy for true ECMP:

1. Launch `proxy2` with `ubuntu-small-mcast` profile + eth2→lxdbr3 device
2. Assign `198.51.100.19/28` + `2001:db8:d::3/64` on its peering NIC
3. Configure BIRD2 with same AnyCast prefix, iBGP peer = `198.51.100.17` (router2)
4. router2 sees two equal paths → installs ECMP
