# bitcoin-shard-proxy integration

The `proxy` VM runs [bitcoin-shard-proxy](https://github.com/lightwebinc/bitcoin-shard-proxy), deployed via the [bitcoin-ingress](https://github.com/lightwebinc/bitcoin-ingress) Ansible playbook.

## Deployed state

| Item       | Value                                                        |
| ---------- | ------------------------------------------------------------ |
| Binary     | `/usr/local/bin/bitcoin-shard-proxy`                         |
| Config     | `/etc/bitcoin-shard-proxy/config.env`                        |
| Service    | `bitcoin-shard-proxy.service` (systemd, enabled)             |
| Listen     | `[::]:9000` UDP — BRC-124/BRC-128 (or legacy BRC-12) frames in |
| Egress     | `enp6s0` → `ff05::/16` (site-local multicast)                |
| Shard bits | `2` (4 groups: `ff05::0`–`ff05::3`)                          |
| Metrics    | `http://10.10.10.20:9100/metrics`                            |
| Health     | `http://10.10.10.20:9100/healthz`                            |
| Readiness  | `http://10.10.10.20:9100/readyz`                             |

## Ansible inventory

Inventory lives in this repo at
[`ansible/ingress-hosts.yml`](../ansible/ingress-hosts.yml) and is
invoked by `ansible/run-deploy.sh`. See
[bitcoin-ingress docs/ansible.md](https://github.com/lightwebinc/bitcoin-ingress/blob/main/docs/ansible.md)
and [docs/lxd-lab.md](https://github.com/lightwebinc/bitcoin-ingress/blob/main/docs/lxd-lab.md)
for full playbook documentation.

```yaml
all:
  children:
    ingress_nodes:
      vars:
        ansible_user: ubuntu
        ansible_connection: ssh
        ansible_ssh_common_args: "-o StrictHostKeyChecking=no"
        egress_mode: ethernet
        shard_bits: 2
        mc_scope: site
        enable_bgp: false
      hosts:
        proxy:
          ansible_host: 10.10.10.20
          egress_iface: enp6s0 # must be host-level var, not group vars
```

> **Note:** `egress_iface` must be set at the host level. Setting it under `vars:` is silently overridden by `group_vars/all.yml` in bitcoin-ingress. See [bitcoin-ingress docs/ansible.md](https://github.com/lightwebinc/bitcoin-ingress/blob/main/docs/ansible.md) for details.

## Deploy / upgrade

```bash
cd /path/to/bitcoin-ingress/ansible
ansible-playbook -i inventory/hosts.yml site.yml

# Upgrade to a specific version
ansible-playbook -i inventory/hosts.yml site.yml --tags proxy -e proxy_version=v1.2.0
```

After deployment, listeners re-emit MLD membership automatically when the
`bitcoin-shard-listener.service` restarts — see
[docs/network.md](network.md#bridge-mdb-volatility).

## Verification

```bash
# Service and health
lxc exec proxy -- systemctl status bitcoin-shard-proxy
lxc exec proxy -- curl -s http://localhost:9100/healthz
lxc exec proxy -- curl -s http://localhost:9100/readyz

# Send BSV frames from the source VM using bitcoin-subtx-generator.
# 1000 pps for 10 s with 8 random subtree IDs (seed pinned in ansible/listener-hosts.yml).
lxc exec source -- subtx-gen \
  -addr '[fd20::2]:9000' \
  -shard-bits 2 -subtrees 8 -subtree-seed 'lax-lab-2026' \
  -pps 1000 -duration 10s

# Confirm forwarded packet counter incremented
lxc exec proxy -- curl -s http://localhost:9100/metrics | grep bsp_packets_forwarded_total

# Capture multicast delivery on a listener
lxc exec listener1 -- tcpdump -i enp6s0 -n 'ip6 and udp' -c 8
```

## Known deployment notes

- Ubuntu 24.04 LXD VMs use predictable NIC names (`enp5s0`, `enp6s0`), not `eth0`/`eth1`.
- The `acl` package must be present on the target VM for Ansible `become` with system users to work.
- The systemd `ExecStartPre` command requires `/bin/sh -c '...'` wrapping — systemd does not perform shell expansion in `ExecStartPre` directly.
- Bridge MDB is volatile. Restart `bitcoin-shard-listener.service` on
  all listeners after any proxy reboot or re-deploy to restore
  multicast delivery.
