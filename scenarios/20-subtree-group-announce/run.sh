#!/usr/bin/env bash
# Scenario 20 — BRC-127 Subtree Group Announcement Dynamic Filtering
#
# End-to-end test of the BRC-127 control plane:
#   source VM → TCP SubtreeAnnounce → proxy → ff05::ff:fffc:9001 (multicast) →
#   listener3 registry → group-based forwarding of all 8 subtrees.
#
# Prerequisites:
#   - Binaries on all VMs rebuilt from BRC-127 source.
#   - Proxy configured with TCP_LISTEN_PORT=9002 (/etc/bitcoin-shard-proxy/config.env).
#   - listener3 configured with SUBTREE_GROUPS=bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf
#     and no SUBTREE_INCLUDE (/etc/bitcoin-shard-listener/config.env).
#   - source VM has new subtx-gen binary.
#
# If deploying from scratch use the Ansible inventories:
#   cd ~/repo/bitcoin-multicast-test/ansible && bash run-deploy.sh
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

# Well-known test group ID — matches subtree_groups in listener-hosts.yml.
TEST_GROUP_ID="bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf"
# Proxy TCP address for SubtreeAnnounce injection.
: "${PROXY_TCP_ADDR:=[fd20::2]:9002}"

# ---------------------------------------------------------------------------
# Optional inline service reconfiguration (skipped when SKIP_RECONFIG=1).
# Set SKIP_RECONFIG=1 if VMs are already deployed with BRC-127 config.
# ---------------------------------------------------------------------------
: "${SKIP_RECONFIG:=0}"
ORIG_L3_SUBTREE_INCLUDE=""
PROXY_TCP_WAS_ZERO=0

restore() {
  if [[ "$SKIP_RECONFIG" -eq 1 ]]; then return; fi
  echo "==> [cleanup] Restore listener3 config"
  lxc exec listener3 -- bash -c "
    sed -i '/^SUBTREE_GROUPS=/d'          /etc/bitcoin-shard-listener/config.env
    sed -i '/^ANNOUNCE_SCOPE=/d'          /etc/bitcoin-shard-listener/config.env
    if [[ -n '$ORIG_L3_SUBTREE_INCLUDE' ]]; then
      echo 'SUBTREE_INCLUDE=$ORIG_L3_SUBTREE_INCLUDE' >> /etc/bitcoin-shard-listener/config.env
    fi
    systemctl restart bitcoin-shard-listener
  " || true
  if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
    echo "==> [cleanup] Disable proxy TCP"
    lxc exec proxy -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/bitcoin-shard-proxy/config.env
      systemctl restart bitcoin-shard-proxy
    " || true
  fi
}
trap restore EXIT

if [[ "$SKIP_RECONFIG" -ne 1 ]]; then
  # --- Enable proxy TCP ---------------------------------------------------
  echo "==> Enable proxy TCP ingress (port 9002)"
  PROXY_TCP_WAS_ZERO=$(lxc exec proxy -- bash -c "
    grep -q '^TCP_LISTEN_PORT=0' /etc/bitcoin-shard-proxy/config.env && echo 1 || echo 0
  ")
  lxc exec proxy -- bash -c "
    if grep -q '^TCP_LISTEN_PORT=' /etc/bitcoin-shard-proxy/config.env; then
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=9002/' /etc/bitcoin-shard-proxy/config.env
    else
      echo 'TCP_LISTEN_PORT=9002' >> /etc/bitcoin-shard-proxy/config.env
    fi
    systemctl restart bitcoin-shard-proxy
  "
  sleep 3

  # --- Configure listener3 for BRC-127 group filter -----------------------
  echo "==> Configure listener3 for BRC-127 group filter (SUBTREE_GROUPS=$TEST_GROUP_ID)"
  ORIG_L3_SUBTREE_INCLUDE=$(lxc exec listener3 -- bash -c "
    grep '^SUBTREE_INCLUDE=' /etc/bitcoin-shard-listener/config.env | cut -d= -f2 || true
  ")
  lxc exec listener3 -- bash -c "
    sed -i '/^SUBTREE_INCLUDE=/d' /etc/bitcoin-shard-listener/config.env
    if grep -q '^SUBTREE_GROUPS=' /etc/bitcoin-shard-listener/config.env; then
      sed -i 's/^SUBTREE_GROUPS=.*/SUBTREE_GROUPS=$TEST_GROUP_ID/' /etc/bitcoin-shard-listener/config.env
    else
      echo 'SUBTREE_GROUPS=$TEST_GROUP_ID' >> /etc/bitcoin-shard-listener/config.env
    fi
    if ! grep -q '^ANNOUNCE_SCOPE=' /etc/bitcoin-shard-listener/config.env; then
      echo 'ANNOUNCE_SCOPE=site' >> /etc/bitcoin-shard-listener/config.env
    fi
    systemctl restart bitcoin-shard-listener
  "
  sleep 5  # allow listener3 to join ff05::ff:fffc:9001 and start eviction loop
fi

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Run generator with SubtreeAnnounce (all 8 subtrees → $TEST_GROUP_ID)"
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr           "$PROXY_ADDR" \
  -shard-bits     "$SHARD_BITS" \
  -subtrees       "$SUBTREES" \
  -subtree-seed   "$SUBTREE_SEED" \
  -pps            "$PPS" \
  -duration       "$DURATION" \
  -payload-size   "$PAYLOAD_SIZE" \
  -announce-addr  "$PROXY_TCP_ADDR" \
  -subtree-group  "$TEST_GROUP_ID" \
  -announce-interval 2s \
  -log-interval   2s 2>&1)
echo "$gen_output" >&2
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1)
frames="${frames:-$(( PPS * $(dur_to_seconds "$DURATION") ))}"
echo "-- sent: $frames --"

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# --- Assertions -------------------------------------------------------------
received_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_frames_received_total)
fwd_l3=$(diff_metric      "$BEFORE" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')
dropped_l3=$(diff_metric  "$BEFORE" "$AFTER" listener3 'bsl_frames_dropped_total|subtree_include_miss')

echo ""
echo "listener3: received=$received_l3  forwarded=$fwd_l3  subtree_include_miss=$dropped_l3"
echo ""

# BRC-127 working: all 8 subtrees announced → ~100% forwarded.
# Baseline (no BRC-127): only 1/8 forwarded — assert would clearly fail.
assert_near "listener3 forwarded (BRC-127 group filter, expect ~100%)" \
  "$fwd_l3" "$received_l3" 0.15

# Very few drops expected — tiny race window before first announce arrives.
# Allow up to 25% as a generous upper bound.
if (( received_l3 > 0 && dropped_l3 * 4 > received_l3 )); then
  echo "FAIL  listener3 subtree_include_miss too high: $dropped_l3 / $received_l3 (> 25%)"
  SCENARIO_FAIL=1
else
  echo "PASS  listener3 subtree_include_miss within bounds: $dropped_l3 (< 25% of $received_l3)"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 20: FAIL"
  exit 1
fi
echo "Scenario 20: PASS"
