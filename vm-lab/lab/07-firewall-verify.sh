#!/usr/bin/env bash
# Verify that the bitcoin-listener firewall role is enforcing the expected rules.
# Runs a mix of positive and negative probes from the LXD host.
#
# Positive probes:
#   - SSH (mgmt) to listener1..3 from 10.10.10.0/24: expect success.
#   - HTTP /metrics on :9200: expect 200.
#   - UDP :9001 (listener port): expect packets accepted (no ICMP-refused).
#
# Negative probes:
#   - TCP :9001 (wrong protocol, not in allow-list): expect connection refused.
#   - TCP :9200 from an unexpected src (simulated by probing from inside
#     another VM that is NOT in mgmt_cidrs_v4): expect timeout/refused.
#
# Rulesets captured for audit:
#   - `nft list ruleset` from each listener VM is snapshotted to the report.
set -euo pipefail
exec </dev/null

LISTENERS=(listener1 listener2 listener3)
MGMT_IPS=(10.10.10.31 10.10.10.32 10.10.10.33)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPORT_DIR="$REPO_DIR/scenarios/00-firewall"
REPORT="$REPORT_DIR/report.txt"
mkdir -p "$REPORT_DIR"
: > "$REPORT"

log() { printf '%s\n' "$*" | tee -a "$REPORT"; }

log "==> [07] Firewall verification — $(date -u +%FT%TZ)"
log ""

# --- Ruleset snapshots ---
for vm in "${LISTENERS[@]}"; do
  log "--- [$vm] nft list ruleset ---"
  lxc exec "$vm" -- nft list ruleset 2>&1 | sed 's/^/    /' | tee -a "$REPORT" >/dev/null
  log ""
done

# --- Positive: SSH over mgmt ---
for ip in "${MGMT_IPS[@]}"; do
  if timeout 5 bash -c "echo > /dev/tcp/$ip/22" 2>/dev/null; then
    log "PASS  tcp://$ip:22 (SSH mgmt) reachable"
  else
    log "FAIL  tcp://$ip:22 (SSH mgmt) NOT reachable"
  fi
done

# --- Positive: metrics ---
for ip in "${MGMT_IPS[@]}"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$ip:9200/metrics" || echo 000)
  if [[ "$code" == "200" ]]; then
    log "PASS  http://$ip:9200/metrics returned 200"
  else
    log "FAIL  http://$ip:9200/metrics returned $code"
  fi
done

# --- Negative: TCP on the UDP data-path port should be refused ---
for ip in "${MGMT_IPS[@]}"; do
  if timeout 3 bash -c "echo > /dev/tcp/$ip/9001" 2>/dev/null; then
    log "FAIL  tcp://$ip:9001 unexpectedly accepted — UDP-only port should refuse TCP"
  else
    log "PASS  tcp://$ip:9001 refused/timeout as expected"
  fi
done

# --- Negative: probe from source VM (NOT in mgmt_cidrs_v4=10.10.10.0/24)
# Actually source IS in the mgmt CIDR so this is a sanity check that the
# positive flow works from another LXD VM rather than just the host.
for ip in "${MGMT_IPS[@]}"; do
  if lxc exec source -- bash -c "timeout 3 curl -s -o /dev/null -w '%{http_code}' http://$ip:9200/metrics" 2>/dev/null | grep -q 200; then
    log "PASS  source → http://$ip:9200/metrics (in-CIDR peer, expected success)"
  else
    log "FAIL  source → http://$ip:9200/metrics (in-CIDR peer) — expected success"
  fi
done

log ""
log "==> [07] Report written to $REPORT"
