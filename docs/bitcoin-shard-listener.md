# bitcoin-shard-listener integration

Each `listener1..3` VM runs
[`bitcoin-shard-listener`](https://github.com/lightwebinc/bitcoin-shard-listener),
deployed via the [`bitcoin-listener`](https://github.com/lightwebinc/bitcoin-listener)
Ansible playbook using the inventory committed to
[`ansible/listener-hosts.yml`](../ansible/listener-hosts.yml).

## Deployed state

| Item       | Value                                                            |
| ---------- | ---------------------------------------------------------------- |
| Binary     | `/usr/local/bin/bitcoin-shard-listener`                          |
| Config     | `/etc/bitcoin-shard-listener/config.env`                         |
| Service    | `bitcoin-shard-listener.service` (systemd)                       |
| UDP listen | `[::]:9001` — multicast groups joined on `enp6s0`                |
| Egress     | `127.0.0.1:9100` UDP (local sink)                                |
| Metrics    | `http://<mgmt-ip>:9200/metrics`                                  |
| Health     | `http://<mgmt-ip>:9200/healthz`                                  |
| Readiness  | `http://<mgmt-ip>:9200/readyz`                                   |
| Firewall   | `enable_firewall=true`, allow-list `10.10.10.0/24` + `fd20::/64` |

## Filter policy per host

Pinned with seed `lax-lab-2026`, pool size 8:

| VM        | `shard_include` | `subtree_include`               | `subtree_exclude`               | `mc_egress_enabled`    | `mc_scope` |
| --------- | --------------- | ------------------------------- | ------------------------------- | ---------------------- | ---------- |
| listener1 | —               | —                               | —                               | `true` (scope: `link`) | `site`     |
| listener2 | `0,1`           | —                               | `836021c2…0700a42` (pool idx 2) | `false`                | `site`     |
| listener3 | —               | `07015348…f956dbb` (pool idx 5) | —                               | `false`                | `site`     |
| listener4 | —               | —                               | —                               | `false`                | `link`     |

Regenerate the pool with:

```bash
subtx-gen -subtrees 8 -subtree-seed 'lax-lab-2026' -print-subtrees
```

## Deploy / upgrade

```bash
bash ansible/run-deploy.sh                           # proxy + listeners
# or just listeners:
cd ~/repo/bitcoin-listener/ansible
ansible-playbook -i ~/repo/bitcoin-multicast-test/ansible/listener-hosts.yml site.yml
```

## Sink capture (optional)

Each listener forwards decoded frames to local UDP `127.0.0.1:9100`.
To inspect raw frames on a specific listener:

```bash
lxc exec listener1 -- /usr/local/bin/sink-test-frames -port 9100 -count 100 -timeout 30s
```

`sink-test-frames` ships with bitcoin-shard-listener; install it onto
each listener by copying the binary from `~/repo/bitcoin-shard-listener/`
or building in place.

## Metrics integration (metrics VM)

The `metrics` VM (10.10.10.142) scrapes listeners on port 9200 and
relabels `instance` to `listener1/2/3`. The canonical scrape config
lives at [`docs/prometheus/prometheus.yml`](prometheus/prometheus.yml)
and is pushed by:

```bash
bash lab/09-metrics-update.sh
```

The same script imports/updates both Grafana dashboards under
[`docs/grafana/`](grafana/) via the Grafana HTTP API.

## Verification

```bash
# Service state across all listeners
for vm in listener1 listener2 listener3 listener4; do
  lxc exec "$vm" -- systemctl is-active bitcoin-shard-listener.service
done

# Scrape a metrics snapshot
curl -s http://10.10.10.31:9200/metrics | grep bsl_frames_received_total

# Run functional scenarios
bash scenarios/01-functional-all-shards/run.sh
bash scenarios/02-functional-shard-filter/run.sh
bash scenarios/03-functional-subtree-filter/run.sh
bash scenarios/05-mc-egress-bridge/run.sh
```

## Known deployment notes

- Ubuntu 24.04 LXD VMs use predictable NIC names (`enp5s0`, `enp6s0`),
  not `eth0`/`eth1`. `ingress_iface` is set per-host in the inventory.
- The `acl` package must be installed on the VM for Ansible `become`
  with system users to work.
- `ingress_iface` must be a **host-level** var: setting it under
  `vars:` is silently overridden by `group_vars/all.yml` in
  bitcoin-listener.
- `mgmt_cidrs_v4` must include the LXD mgmt CIDR
  (`10.10.10.0/24`) or the firewall will block both SSH and
  Prometheus scrapes.
