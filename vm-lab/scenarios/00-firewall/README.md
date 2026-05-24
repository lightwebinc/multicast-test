# Scenario 00 — Firewall verification

Runs [`lab/07-firewall-verify.sh`](../../lab/07-firewall-verify.sh) which:

- Snapshots `nft list ruleset` from each listener into `report.txt`.
- **Positive probes** (must pass): SSH :22, HTTP :9200 from LXD host and
  from the `source` VM (both inside `mgmt_cidrs_v4=10.10.10.0/24`).
- **Negative probes** (must fail): TCP :9001 on the UDP-only data-path
  port should be refused.

Pass criteria: the report contains no `FAIL` lines.

## Run

```bash
bash run.sh
```
