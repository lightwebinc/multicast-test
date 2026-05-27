# Scenario 99 — NACK / retransmit (active)

**Status:** active as of 2026-05-04. See [`expected.md`](expected.md) for the
pass criteria and the most recent run, and run [`run.sh`](run.sh) to execute.

Topology requires a `retry1` VM (provisioned by `lab/03-launch.sh`,
`lab/06-netplan.sh`, etc.) and the `retransmission-infra` ansible
playbook deployed via:

```bash
cd ~/repo/retransmission-infra/ansible
ansible-playbook -i ~/repo/multicast-test/ansible/retry-hosts.yml site.yml
```

Listeners must be re-deployed (or `--tags firewall,listener`) after
`retry_endpoints` is set per-host in `listener-hosts.yml`, otherwise the
listener firewall blocks outbound NACK UDP.

## Intended design

Drive `subtx-gen` with gap injection:

```bash
lxc exec source -- subtx-gen \
  -addr '[fd20::2]:9000' \
  -shard-bits 2 -subtrees 8 -subtree-seed 'multicast-lab-bsv' \
  -pps 1000 -duration 30s \
  -seq-gap-every 500 \
  -seq-gap-size 1 \
  -seq-gap-delay 50ms     # 0 = permanent gap
```

Assertions (all listeners):

| Metric | Expected with `-seq-gap-delay 50ms` | With permanent gap (`-seq-gap-delay 0`) |
|-------------------------------|----------------------------------------|-----------------------------------------|
| `bsl_gaps_detected_total` | > 0 (roughly `frames / seq_gap_every`) | same |
| `bsl_nacks_dispatched_total` | > 0 | > 0 |
| `bsl_gaps_suppressed_total` | ≈ `gaps_detected` | 0 |
| `bsl_gaps_unrecovered_total` | 0 | ≈ `gaps_detected` x NACK_MAX_RETRIES |

## Activation checklist (once retry-endpoint exists)

1. Deploy `retry-endpoint` to a new VM (e.g. `retry1`).
2. Set `retry_endpoints: "10.10.10.<retry-ip>:9300"` in
   `ansible/listener-hosts.yml` group vars.
3. Re-run `ansible/run-deploy.sh` and `lab/09-metrics-update.sh`.
4. Move this placeholder into a real `run.sh` using the generator command
   above, with the four assertions listed.
