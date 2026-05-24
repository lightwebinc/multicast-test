#!/usr/bin/env bash
# Scenario 13 — MISS escalation by tier and preference.
#
# Verifies that the listener gap tracker walks the beacon-ordered registry:
#   retry1 (T0/P128) → MISS, retry2 (T0/P64) → MISS, retry3 (T1/P128) → ACK
#
# Preconditions:
#   retry1 mgmt=10.10.10.34  fabric=fd20::24  Tier=0 Pref=128
#   retry2 mgmt=10.10.10.35  fabric=fd20::25  Tier=0 Pref=64
#   retry3 mgmt=10.10.10.36  fabric=fd20::26  Tier=1 Pref=128
#   All three are deployed and healthy; beacon_interval=5s on each.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PPS:=1000}"
: "${DURATION:=15s}"

RETRY1_VM=retry1; RETRY1_IP=10.10.10.34; RETRY1_METRICS_PORT=9400
RETRY2_VM=retry2; RETRY2_IP=10.10.10.35; RETRY2_METRICS_PORT=9400
RETRY3_VM=retry3; RETRY3_IP=10.10.10.36; RETRY3_METRICS_PORT=9400

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
R1_BEFORE="$SCENARIO_DIR/retry1.before.tsv"
R1_AFTER="$SCENARIO_DIR/retry1.after.tsv"
R2_BEFORE="$SCENARIO_DIR/retry2.before.tsv"
R2_AFTER="$SCENARIO_DIR/retry2.after.tsv"
R3_BEFORE="$SCENARIO_DIR/retry3.before.tsv"
R3_AFTER="$SCENARIO_DIR/retry3.after.tsv"

RETRY_METRICS=(bre_frames_received_total bre_frames_cached_total bre_frames_dropped_total
               bre_nack_requests_total bre_retransmits_total bre_retransmit_dedup_total
               bre_cache_hits_total bre_cache_misses_total bre_rate_limit_drops_total)

snapshot_retry() {
  local vm="$1" ip="$2" port="$3" out="$4"
  : > "$out"
  for m in "${RETRY_METRICS[@]}"; do
    printf '%s\t%s\t%s\n' "$vm" "$m" "$(metric_value "$ip:$port" "$m")" >> "$out"
  done
}

retry_diff() {
  local before="$1" after="$2" metric="$3"
  local b a
  b=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$before")
  a=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$after")
  echo $(( ${a:-0} - ${b:-0} ))
}

# --- Verify endpoints are healthy ----------------------------------------
echo "==> Checking endpoint health..."
for spec in "$RETRY1_IP:$RETRY1_METRICS_PORT" "$RETRY2_IP:$RETRY2_METRICS_PORT" "$RETRY3_IP:$RETRY3_METRICS_PORT"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$spec/healthz" || echo 000)
  if [[ "$code" != "200" ]]; then
    echo "FAIL  http://$spec/healthz returned $code — endpoint not ready"
    exit 1
  fi
  echo "     $spec: healthy"
done

# --- Flush caches on retry1/retry2; clear stale gap state on listeners --------
# Listeners are restarted FIRST so they are running when retry1/retry2 restart
# and emit their immediate startup beacon. This ensures all three endpoints land
# in the listener registry within one 5s beacon cycle.
# Without listener restart, background NACKs from prior gaps escalate to retry3
# (retry1/retry2 blocked) and can overwhelm it during run-all.
echo "==> Restarting listeners to clear stale gap-tracker state..."
for lvm in listener1 listener2 listener3; do
  lxc exec "$lvm" -- systemctl restart bitcoin-shard-listener
done

echo "==> Waiting for listeners to be ready..."
for lvm_ip in "listener1:10.10.10.31" "listener2:10.10.10.32" "listener3:10.10.10.33"; do
  lvm="${lvm_ip%%:*}"; lip="${lvm_ip##*:}"
  for i in $(seq 1 20); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://$lip:9200/healthz" || echo 000)
    if [[ "$code" == "200" ]]; then
      echo "     $lvm: ready"
      break
    fi
    if [[ "$i" -eq 20 ]]; then
      echo "FAIL  $lvm did not become ready within 60s"
      exit 1
    fi
    sleep 3
  done
done

echo "==> Restarting retry1 and retry2 to flush in-memory caches..."
lxc exec "$RETRY1_VM" -- systemctl restart bitcoin-retry-endpoint
lxc exec "$RETRY2_VM" -- systemctl restart bitcoin-retry-endpoint

echo "==> Waiting for retry1 and retry2 to be ready..."
for vm_ip_port in "$RETRY1_VM $RETRY1_IP:$RETRY1_METRICS_PORT" "$RETRY2_VM $RETRY2_IP:$RETRY2_METRICS_PORT"; do
  vm="${vm_ip_port%% *}"; ep="${vm_ip_port##* }"
  for i in $(seq 1 20); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://$ep/readyz" || echo 000)
    if [[ "$code" == "200" ]]; then
      echo "     $vm: ready"
      break
    fi
    if [[ "$i" -eq 20 ]]; then
      echo "FAIL  $vm did not become ready within 60s"
      exit 1
    fi
    sleep 3
  done
done

# --- Block multicast ingress on retry1 and retry2 ------------------------
# Cleanup trap: always remove the blocking rules, even on failure.
cleanup() {
  remove_listener_loss
  echo "==> Cleanup: removing ip6tables ingress blocks on retry1 and retry2..."
  lxc exec "$RETRY1_VM" -- ip6tables -D INPUT -i enp6s0 -p udp --dport 9001 -j DROP 2>/dev/null || true
  lxc exec "$RETRY2_VM" -- ip6tables -D INPUT -i enp6s0 -p udp --dport 9001 -j DROP 2>/dev/null || true
}
trap cleanup EXIT

echo "==> Blocking multicast ingress on retry1 and retry2 (port 9001)..."
lxc exec "$RETRY1_VM" -- ip6tables -I INPUT 1 -i enp6s0 -p udp --dport 9001 -j DROP
lxc exec "$RETRY2_VM" -- ip6tables -I INPUT 1 -i enp6s0 -p udp --dport 9001 -j DROP
echo "     retry1 and retry2 will respond MISS (cache empty)"
echo "     retry3 continues ingesting multicast normally (cache warm)"

# --- Wait for beacon discovery -------------------------------------------
# With beacon_interval=5s, two full cycles give listeners ≥12s to populate
# the registry with correct tier/preference ordering from all three endpoints.
echo "==> Waiting 12s for beacon discovery to converge on all listeners..."
sleep 12

echo "==> Injecting selective frame loss on listeners (1%) to create gaps"
apply_listener_loss "1%"

# --- Snapshot before ------------------------------------------------------
echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_retry "$RETRY1_VM" "$RETRY1_IP" "$RETRY1_METRICS_PORT" "$R1_BEFORE"
snapshot_retry "$RETRY2_VM" "$RETRY2_IP" "$RETRY2_METRICS_PORT" "$R2_BEFORE"
snapshot_retry "$RETRY3_VM" "$RETRY3_IP" "$RETRY3_METRICS_PORT" "$R3_BEFORE"

# --- Run generator -------------------------------------------------------
echo "==> Running generator at pps=$PPS duration=$DURATION"
echo "    retry3 warms cache from multicast; retry1/retry2 cache stays empty."
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION" \
  -payload-size "$PAYLOAD_SIZE" \
  -log-interval 5s 2>&1)
echo "$gen_output" | tail -5
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1 || true)
echo "    sent=${frames:-?} frames"

# --- Drain ---------------------------------------------------------------
echo "==> Allow NACK/retransmit pipeline to drain (5s)..."
sleep 5

# --- Snapshot after -------------------------------------------------------
echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_retry "$RETRY1_VM" "$RETRY1_IP" "$RETRY1_METRICS_PORT" "$R1_AFTER"
snapshot_retry "$RETRY2_VM" "$RETRY2_IP" "$RETRY2_METRICS_PORT" "$R2_AFTER"
snapshot_retry "$RETRY3_VM" "$RETRY3_IP" "$RETRY3_METRICS_PORT" "$R3_AFTER"

# --- Compute diffs --------------------------------------------------------
sum_listener_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

gaps_detected=$(sum_listener_metric bsl_gaps_detected_total)
nacks_dispatched=$(sum_listener_metric bsl_nacks_dispatched_total)
gaps_suppressed=$(sum_listener_metric bsl_gaps_suppressed_total)
gaps_unrecovered=$(sum_listener_metric bsl_gaps_unrecovered_total)

r1_cached=$(retry_diff "$R1_BEFORE" "$R1_AFTER" bre_frames_cached_total)
r1_nacks=$(retry_diff "$R1_BEFORE" "$R1_AFTER" bre_nack_requests_total)
r1_misses=$(retry_diff "$R1_BEFORE" "$R1_AFTER" bre_cache_misses_total)

r2_cached=$(retry_diff "$R2_BEFORE" "$R2_AFTER" bre_frames_cached_total)
r2_nacks=$(retry_diff "$R2_BEFORE" "$R2_AFTER" bre_nack_requests_total)
r2_misses=$(retry_diff "$R2_BEFORE" "$R2_AFTER" bre_cache_misses_total)

r3_cached=$(retry_diff "$R3_BEFORE" "$R3_AFTER" bre_frames_cached_total)
r3_hits=$(retry_diff "$R3_BEFORE" "$R3_AFTER" bre_cache_hits_total)
r3_retransmits=$(retry_diff "$R3_BEFORE" "$R3_AFTER" bre_retransmits_total)
r3_dedup=$(retry_diff "$R3_BEFORE" "$R3_AFTER" bre_retransmit_dedup_total)

cat <<EOF

-- Listener aggregate (l1+l2+l3) --
bsl_gaps_detected_total    = $gaps_detected
bsl_nacks_dispatched_total = $nacks_dispatched
bsl_gaps_suppressed_total  = $gaps_suppressed
bsl_gaps_unrecovered_total = $gaps_unrecovered

-- retry1 (T0/P128, ingress BLOCKED) --
bre_frames_cached_total    = $r1_cached
bre_nack_requests_total    = $r1_nacks
bre_cache_misses_total     = $r1_misses

-- retry2 (T0/P64, ingress BLOCKED) --
bre_frames_cached_total    = $r2_cached
bre_nack_requests_total    = $r2_nacks
bre_cache_misses_total     = $r2_misses

-- retry3 (T1/P128, cache WARM) --
bre_frames_cached_total    = $r3_cached
bre_cache_hits_total       = $r3_hits
bre_retransmits_total      = $r3_retransmits
bre_retransmit_dedup_total = $r3_dedup
EOF

# --- Assertions -----------------------------------------------------------
SCENARIO_FAIL=${SCENARIO_FAIL:-0}

# Gaps must be detected (natural delivery loss at 1000 pps on LXD bridge).
if [[ "$gaps_detected" -le 0 ]]; then
  echo "FAIL  listeners detected no gaps — natural loss insufficient or bridge over-provisioned?"
  SCENARIO_FAIL=1
else
  echo "PASS  listeners detected $gaps_detected gaps"
fi

# NACKs must be dispatched.
if [[ "$nacks_dispatched" -le 0 ]]; then
  echo "FAIL  listeners dispatched no NACKs"
  SCENARIO_FAIL=1
else
  echo "PASS  listeners dispatched $nacks_dispatched NACKs"
fi

# retry1 must have received NACKs (confirms it is first in sorted order).
if [[ "$r1_nacks" -le 0 ]]; then
  echo "FAIL  retry1 received no NACKs — beacon discovery may not have established T0/P128 ordering"
  SCENARIO_FAIL=1
else
  echo "PASS  retry1 received $r1_nacks NACKs (correct: T0/P128 = first in priority order)"
fi

# retry1 must have cache misses (ingress was blocked → no frames cached).
if [[ "$r1_misses" -le 0 ]]; then
  echo "FAIL  retry1 had no cache misses — ingress block may not have worked"
  SCENARIO_FAIL=1
else
  echo "PASS  retry1 had $r1_misses cache misses (MISS responses sent)"
fi

# retry2 must have received NACKs (escalated from retry1 MISS).
if [[ "$r2_nacks" -le 0 ]]; then
  echo "FAIL  retry2 received no NACKs — MISS escalation from retry1 did not occur"
  SCENARIO_FAIL=1
else
  echo "PASS  retry2 received $r2_nacks NACKs (escalated from retry1 MISS)"
fi

# retry2 must have cache misses (ingress was blocked → no frames cached).
if [[ "$r2_misses" -le 0 ]]; then
  echo "FAIL  retry2 had no cache misses — ingress block may not have worked"
  SCENARIO_FAIL=1
else
  echo "PASS  retry2 had $r2_misses cache misses (MISS responses sent)"
fi

# retry3 must have cached frames (it was NOT blocked).
if [[ "$r3_cached" -le 0 ]]; then
  echo "FAIL  retry3 cached no frames — multicast ingress to retry3 may be broken"
  SCENARIO_FAIL=1
else
  echo "PASS  retry3 cached $r3_cached frames"
fi

# retry3 must have served NACKs from cache (final escalation target).
if [[ "$r3_hits" -le 0 ]]; then
  echo "FAIL  retry3 had no cache hits — escalation did not reach retry3, or cache was cold"
  SCENARIO_FAIL=1
else
  echo "PASS  retry3 answered $r3_hits NACKs from cache (ACK responses)"
fi

# retry3 must have retransmitted.
if [[ "$r3_retransmits" -le 0 ]]; then
  echo "FAIL  retry3 did not retransmit any frames"
  SCENARIO_FAIL=1
else
  echo "PASS  retry3 retransmitted $r3_retransmits frames"
fi

# retry3 dedup must fire: all 3 listeners escalate to retry3 for the same gaps;
# the first NACK triggers SetNX (succeeds), subsequent NACKs for the same SeqNum
# are suppressed. Dedup count ≈ (num_listeners - 1) x recovered_gaps.
if [[ "$r3_dedup" -le 0 ]]; then
  echo "FAIL  retry3 had no dedup fires (Redis not configured, or only one listener escalating?)"
  SCENARIO_FAIL=1
else
  echo "PASS  retry3 deduped $r3_dedup redundant retransmits via Redis SetNX"
fi

# Almost all gaps must be resolved (retry3 served them). A small number may be
# unrecovered due to LXD bridge UDP loss: if the ACK from retry3 is dropped on all
# 3 of its round-robin slots (positions 2, 5, 8 with MaxRetries=9), the gap is
# marked unrecovered. With ~15% bridge loss, P(3 drops) ≈ 0.3% per gap-listener.
# Limit: 4% of detected gaps (floor 10).
# - Natural noise (standalone):   <1%   (1-7 unrecovered, 0.4% of ~1566 detected)
# - Run-all noise (with restart):  <1%   (listeners restarted → no stale gap state)
# - Real escalation failure:      >10%  (249/1629=15.3% when escalation is broken)
unrecovered_limit=$(( gaps_detected * 4 / 100 ))
[[ "$unrecovered_limit" -lt 10 ]] && unrecovered_limit=10
if [[ "$gaps_unrecovered" -gt "$unrecovered_limit" ]]; then
  echo "FAIL  $gaps_unrecovered gaps unrecovered (limit $unrecovered_limit) — escalation to retry3 broken?"
  SCENARIO_FAIL=1
else
  echo "PASS  gaps_unrecovered=$gaps_unrecovered (within tolerance of $unrecovered_limit)"
fi

# retry1 and retry2 must NOT have cached any frames (ingress was blocked).
if [[ "$r1_cached" -ne 0 ]]; then
  echo "WARN  retry1 cached $r1_cached frames despite ingress block — check ip6tables rule"
fi
if [[ "$r2_cached" -ne 0 ]]; then
  echo "WARN  retry2 cached $r2_cached frames despite ingress block — check ip6tables rule"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 13: FAIL"
  exit 1
fi
echo "Scenario 13: PASS"
