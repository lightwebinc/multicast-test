#!/usr/bin/env bash
# Scenario 21 — BRC-127 Subtree Group Membership Ramp
#
# Demonstrates and verifies time-varying BRC-127 group membership:
#   - listener3 configured with SUBTREE_GROUPS but NO static SUBTREE_INCLUDE
#   - subtx-gen announces subtrees one-by-one every ANNOUNCE_PHASE_INTERVAL
#   - BRC-124 frames (all 8 subtrees) are sent continuously throughout
#
# Three-phase structure:
#   Initial silence  (T=0–1h):    0 subtrees in group  → ~100% drops
#   Ramp             (T=1h–8h):   1→8 subtrees         → 12.5%→100% forwarded
#   Stable           (T=8h–24h):  8 subtrees           → ~100% forwarded
#   Drain            (T=24h+):    announcements stop   → TTL evictions fire
#
# Assertions:
#   1. Control plane: subtree_announces_received_total > 0
#   2. Negative delivery: during initial silence, dropped_include_miss ≈ received
#   3. Positive delivery: during stable phase, forwarded ≈ received
#   4. TTL eviction: subtree_group_evictions_total > 0 after drain
#   5. Registry empty: bsl_subtree_group_entries == 0 after drain
#
# Prerequisites:
#   - Proxy configured with TCP_LISTEN_PORT=9002, or SKIP_RECONFIG=0 (default).
#   - listener3 will be configured inline (restored on EXIT).
#   - subtx-gen on source VM rebuilt from current source (supports phased mode).
#
# Run time: ~24.5 hours.  Excluded from run-all.sh by default (SKIP_ALWAYS).
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
EARLY="$SCENARIO_DIR/metrics.early.tsv"
MID="$SCENARIO_DIR/metrics.mid.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
AFTER_DRAIN="$SCENARIO_DIR/metrics.after-drain.tsv"

# Well-known group ID — matches listener3 SUBTREE_GROUPS in listener-hosts.yml.
TEST_GROUP_ID="bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf"
# Proxy TCP address for SubtreeAnnounce injection.
: "${PROXY_TCP_ADDR:=[2001:db8:ffff::1]:9002}"

# Timing parameters (overrideable).
: "${GEN_DURATION:=24h}"           # total generator run time
: "${ANNOUNCE_PHASE_SIZE:=1}"      # subtrees to add per phase tick
: "${ANNOUNCE_PHASE_INTERVAL:=1h}" # how often to add the next subtree (8 subtrees over 8h)
: "${ANNOUNCE_INTERVAL:=5m}"       # re-announce period (TTL refresh)
: "${ANNOUNCE_TTL:=900}"           # seconds (15 min); entries expire 15 min after last announce
: "${DRAIN_WAIT:=1800}"            # seconds (30 min); 2x TTL for safety margin

# Derived snapshot timing:
#   EARLY snapshot at T=30min (before first phase tick at T=1h → 0 subtrees)
#   MID   snapshot at T=9h    (1h after all 8 subtrees added at T=8h → stable)
#   wait for generator exit   (T=24h)
EARLY_SLEEP=1800          # 30 min
MID_SLEEP=30600           # 8.5h more → total 1800+30600=32400s=9h from generator start

# ---------------------------------------------------------------------------
# Optional inline service reconfiguration (skipped when SKIP_RECONFIG=1).
# ---------------------------------------------------------------------------
: "${SKIP_RECONFIG:=0}"
ORIG_L3_SUBTREE_INCLUDE=""
PROXY_TCP_WAS_ZERO=0

restore() {
  if [[ "$SKIP_RECONFIG" -eq 1 ]]; then return; fi
  echo "==> [cleanup] Restore listener3 config"
  lxc exec listener3 -- bash -c "
    sed -i '/^SUBTREE_GROUPS=/d'          /etc/shard-listener/config.env
    sed -i '/^ANNOUNCE_SCOPE=/d'          /etc/shard-listener/config.env
    if [[ -n '${ORIG_L3_SUBTREE_INCLUDE}' ]]; then
      echo 'SUBTREE_INCLUDE=${ORIG_L3_SUBTREE_INCLUDE}' >> /etc/shard-listener/config.env
    fi
    systemctl restart shard-listener
  " || true
  if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
    echo "==> [cleanup] Disable proxy TCP"
    lxc exec proxy -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/shard-proxy/config.env
      systemctl restart shard-proxy
    " || true
  fi
}
trap restore EXIT

if [[ "$SKIP_RECONFIG" -ne 1 ]]; then
  # --- Enable proxy TCP -------------------------------------------------------
  echo "==> Enable proxy TCP ingress (port 9002)"
  PROXY_TCP_WAS_ZERO=$(lxc exec proxy -- bash -c "
    grep -q '^TCP_LISTEN_PORT=0' /etc/shard-proxy/config.env && echo 1 || echo 0
  ")
  lxc exec proxy -- bash -c "
    if grep -q '^TCP_LISTEN_PORT=' /etc/shard-proxy/config.env; then
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=9002/' /etc/shard-proxy/config.env
    else
      echo 'TCP_LISTEN_PORT=9002' >> /etc/shard-proxy/config.env
    fi
    systemctl restart shard-proxy
  "
  sleep 3

  # --- Configure listener3 ----------------------------------------------------
  echo "==> Configure listener3: SUBTREE_GROUPS=$TEST_GROUP_ID (no SUBTREE_INCLUDE)"
  ORIG_L3_SUBTREE_INCLUDE=$(lxc exec listener3 -- bash -c "
    grep '^SUBTREE_INCLUDE=' /etc/shard-listener/config.env | cut -d= -f2 || true
  ")
  lxc exec listener3 -- bash -c "
    sed -i '/^SUBTREE_INCLUDE=/d'  /etc/shard-listener/config.env
    if grep -q '^SUBTREE_GROUPS=' /etc/shard-listener/config.env; then
      sed -i 's/^SUBTREE_GROUPS=.*/SUBTREE_GROUPS=$TEST_GROUP_ID/' /etc/shard-listener/config.env
    else
      echo 'SUBTREE_GROUPS=$TEST_GROUP_ID' >> /etc/shard-listener/config.env
    fi
    if ! grep -q '^ANNOUNCE_SCOPE=' /etc/shard-listener/config.env; then
      echo 'ANNOUNCE_SCOPE=site' >> /etc/shard-listener/config.env
    fi
    systemctl restart shard-listener
  "
  sleep 5
fi

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

# ---------------------------------------------------------------------------
# Run generator in the background; take timed mid-run snapshots.
# ---------------------------------------------------------------------------
echo "==> Start generator (duration=$GEN_DURATION, pps=$PPS, subtrees=$SUBTREES)"
echo "    phase-size=$ANNOUNCE_PHASE_SIZE  phase-interval=$ANNOUNCE_PHASE_INTERVAL"
echo "    announce-interval=$ANNOUNCE_INTERVAL  ttl=${ANNOUNCE_TTL}s"
GEN_START=$(date +%s)

lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr              "$PROXY_ADDR"            \
  -shard-bits        "$SHARD_BITS"            \
  -subtrees          "$SUBTREES"              \
  -subtree-seed      "$SUBTREE_SEED"          \
  -pps               "$PPS"                   \
  -duration          "$GEN_DURATION"          \
  -payload-size      "$PAYLOAD_SIZE"          \
  -announce-addr     "$PROXY_TCP_ADDR"        \
  -subtree-group     "$TEST_GROUP_ID"         \
  -announce-interval "$ANNOUNCE_INTERVAL"     \
  -announce-phase-size "$ANNOUNCE_PHASE_SIZE" \
  -announce-phase-interval "$ANNOUNCE_PHASE_INTERVAL" \
  -announce-ttl      "$ANNOUNCE_TTL"          \
  -log-interval      10s &
GEN_PID=$!

# -- EARLY snapshot (T≈70s, before first phase tick → 0 subtrees active) -----
echo "==> Sleeping ${EARLY_SLEEP}s for EARLY snapshot (0-subtree window)..."
sleep "$EARLY_SLEEP"
echo "==> Snapshot metrics (early — 0 subtrees announced)"
snapshot_metrics "$EARLY"

# -- MID snapshot (T≈555s, all 8 subtrees announced and stable) ---------------
echo "==> Sleeping ${MID_SLEEP}s for MID snapshot (all subtrees active)..."
sleep "$MID_SLEEP"
echo "==> Snapshot metrics (mid — all subtrees in group)"
snapshot_metrics "$MID"

# -- Wait for generator to exit (T=720s) --------------------------------------
echo "==> Waiting for generator to finish..."
wait "$GEN_PID" || true
GEN_ELAPSED=$(( $(date +%s) - GEN_START ))
echo "-- generator finished after ${GEN_ELAPSED}s --"

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after generator)"
snapshot_metrics "$AFTER"

# ---------------------------------------------------------------------------
# Drain phase: announcements have stopped; TTL=90s entries will expire.
# ---------------------------------------------------------------------------
echo "==> Drain phase: waiting ${DRAIN_WAIT}s for TTL=${ANNOUNCE_TTL}s expiry..."
sleep "$DRAIN_WAIT"

echo "==> Snapshot metrics (after drain)"
snapshot_metrics "$AFTER_DRAIN"

# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------
echo ""
echo "===== Assertion results ====="
echo ""

# 1. Control plane: announces were received
ann_received=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_subtree_announces_received_total)
echo "listener3: subtree_announces_received=$ann_received"
if (( ann_received > 0 )); then
  echo "PASS  control plane: subtree_announces_received > 0 ($ann_received)"
else
  echo "FAIL  control plane: subtree_announces_received == 0"
  SCENARIO_FAIL=1
fi

# 2. Negative delivery: during initial 0-subtree window, frames were dropped
received_early=$(diff_metric "$BEFORE" "$EARLY" listener3 bsl_frames_received_total)
dropped_early=$(diff_metric  "$BEFORE" "$EARLY" listener3 'bsl_frames_dropped_total|subtree_include_miss')
fwd_early=$(diff_metric      "$BEFORE" "$EARLY" listener3 'bsl_frames_forwarded_total|proto="udp"')
echo ""
echo "listener3 EARLY window (0 subtrees): received=$received_early  dropped=$dropped_early  forwarded=$fwd_early"
if (( received_early > 0 )); then
  assert_near "negative delivery: dropped_include_miss ≈ received (0-subtree window)" \
    "$dropped_early" "$received_early" 0.20
else
  echo "WARN  no frames received during EARLY window (check generator startup)"
fi

# 3. Positive delivery: during stable phase (MID→AFTER), ~100% forwarded
received_stable=$(diff_metric "$MID" "$AFTER" listener3 bsl_frames_received_total)
fwd_stable=$(diff_metric      "$MID" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')
dropped_stable=$(diff_metric  "$MID" "$AFTER" listener3 'bsl_frames_dropped_total|subtree_include_miss')
echo ""
echo "listener3 STABLE window (all subtrees): received=$received_stable  forwarded=$fwd_stable  dropped=$dropped_stable"
if (( received_stable > 0 )); then
  assert_near "positive delivery: forwarded ≈ received (all-subtrees stable phase)" \
    "$fwd_stable" "$received_stable" 0.15
else
  echo "WARN  no frames received during STABLE window (check timing)"
fi

# 4. TTL eviction fired after drain
evictions=$(diff_metric "$AFTER" "$AFTER_DRAIN" listener3 bsl_subtree_group_evictions_total)
echo ""
echo "listener3 DRAIN: evictions=$evictions"
if (( evictions > 0 )); then
  echo "PASS  TTL eviction fired: subtree_group_evictions=$evictions > 0"
else
  echo "FAIL  TTL eviction did not fire (drain_wait=${DRAIN_WAIT}s, ttl=${ANNOUNCE_TTL}s)"
  SCENARIO_FAIL=1
fi

# 5. Registry empty after drain (live gauge read)
LISTENER3_IP="${LISTENER_IPS[2]}"
entries_live=$(metric_value "$LISTENER3_IP:$METRICS_PORT" bsl_subtree_group_entries)
echo ""
echo "listener3 bsl_subtree_group_entries (live gauge) = $entries_live"
if [[ "$entries_live" -eq 0 ]]; then
  echo "PASS  registry empty after drain: bsl_subtree_group_entries=0"
else
  echo "FAIL  registry not empty after drain: bsl_subtree_group_entries=$entries_live"
  SCENARIO_FAIL=1
fi

echo ""
if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 21: FAIL"
  exit 1
fi
echo "Scenario 21: PASS"
