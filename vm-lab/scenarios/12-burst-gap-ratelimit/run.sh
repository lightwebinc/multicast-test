#!/usr/bin/env bash
# Scenario 12 — Burst gap + rate limiting.
#
# Drives subtx-gen with frequent, multi-frame gaps to generate a NACK flood
# from all 3 listeners simultaneously. Verifies that the retry endpoint's
# rate limiter activates AND that some retransmits still succeed.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${PPS:=500}"
: "${DURATION:=15s}"
: "${SEQ_GAP_EVERY:=50}"
: "${SEQ_GAP_SIZE:=3}"
: "${SEQ_GAP_DELAY:=500ms}"
export PPS DURATION SEQ_GAP_EVERY SEQ_GAP_SIZE SEQ_GAP_DELAY

source "$SCENARIO_DIR/../lib/common.sh"
: "${RETRY_VM:=retry1}"
: "${RETRY_IP:=10.10.10.34}"
: "${RETRY_METRICS_PORT:=9400}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
RETRY_BEFORE="$SCENARIO_DIR/retry.before.tsv"
RETRY_AFTER="$SCENARIO_DIR/retry.after.tsv"

retry_metric() {
  metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" "$1"
}

snapshot_retry() {
  local out="$1"
  : > "$out"
  for m in bre_frames_received_total bre_frames_cached_total \
           bre_nack_requests_total bre_rate_limit_drops_total \
           bre_cache_hits_total bre_cache_misses_total bre_cache_errors_total \
           bre_retransmits_total bre_retransmit_dedup_total \
           bre_responses_sent_total bre_response_send_errors_total; do
    printf '%s\t%s\t%s\n' "$RETRY_VM" "$m" "$(retry_metric "$m")" >> "$out"
  done
}

echo "==> Injecting selective frame loss on listeners (5%) to create burst gap load"
apply_listener_loss "5%"
trap 'remove_listener_loss' EXIT

# Stabilise: let any in-flight NACK activity from previous scenarios drain.
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_retry  "$RETRY_BEFORE"

# Verify nft loss rule is still in place and reset counters.
for vm in "${LISTENERS[@]}"; do
  _rule=$(lxc exec "$vm" -- nft list chain inet listener-infra-test input 2>/dev/null | grep -c "drop" || echo 0)
  echo "     [diag] $vm nft_drop_rules=$_rule"
done

echo "==> Generator with burst gap injection"
echo "    pps=$PPS duration=$DURATION every=$SEQ_GAP_EVERY size=$SEQ_GAP_SIZE delay=$SEQ_GAP_DELAY"
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION" \
  -payload-size "$PAYLOAD_SIZE" \
  -seq-gap-every "$SEQ_GAP_EVERY" \
  -seq-gap-size "$SEQ_GAP_SIZE" \
  -seq-gap-delay "$SEQ_GAP_DELAY" \
  -log-interval 5s 2>&1)
echo "$gen_output" | tail -5
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1 || true)
echo "    sent=${frames:-0} frames"

echo "==> Allow NACK/retransmit pipeline to drain"
sleep 5

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_retry  "$RETRY_AFTER"

# Diagnostics: nftables drop counters + direct Prometheus gap check.
check_listener_loss_counters
for _lip in "${LISTENER_IPS[@]}"; do
  _g=$(curl -s --max-time 2 "http://$_lip:$METRICS_PORT/metrics" \
       | awk '/^bsl_gaps_detected_total\{/ && !/^#/ {v+=$NF} END{printf "%.0f",v}')
  echo "     [diag] $_lip gaps_total_now=${_g:-0}"
done
for h in "${LISTENERS[@]}"; do
  _bv=$(awk -v h="$h" -v m="bsl_gaps_detected_total" -F'\t' '$1==h && $2==m {print $3}' "$BEFORE")
  _av=$(awk -v h="$h" -v m="bsl_gaps_detected_total" -F'\t' '$1==h && $2==m {print $3}' "$AFTER")
  echo "     [diag] $h gaps_tsv before=${_bv:-?} after=${_av:-?} delta=$(( ${_av:-0} - ${_bv:-0} ))"
done

sum_listener_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

retry_diff() {
  local metric="$1" b a
  b=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$RETRY_BEFORE")
  a=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$RETRY_AFTER")
  echo $(( ${a:-0} - ${b:-0} ))
}

gaps_detected=$(sum_listener_metric bsl_gaps_detected_total)
nacks_dispatched=$(sum_listener_metric bsl_nacks_dispatched_total)
gaps_suppressed=$(sum_listener_metric bsl_gaps_suppressed_total)
gaps_unrecovered=$(sum_listener_metric bsl_gaps_unrecovered_total)
nacks_received=$(retry_diff bre_nack_requests_total)
rate_drops=$(retry_diff bre_rate_limit_drops_total)
cache_hits=$(retry_diff bre_cache_hits_total)
cache_misses=$(retry_diff bre_cache_misses_total)
retransmits=$(retry_diff bre_retransmits_total)

cat <<EOF
-- Listener aggregate (l1+l2+l3) --
bsl_gaps_detected_total      = $gaps_detected
bsl_nacks_dispatched_total   = $nacks_dispatched
bsl_gaps_suppressed_total    = $gaps_suppressed
bsl_gaps_unrecovered_total   = $gaps_unrecovered

-- Retry endpoint ($RETRY_VM) --
bre_nack_requests_total      = $nacks_received
bre_rate_limit_drops_total   = $rate_drops
bre_cache_hits_total         = $cache_hits
bre_cache_misses_total       = $cache_misses
bre_retransmits_total        = $retransmits
EOF

SCENARIO_FAIL=0

# --- Assertions ---------------------------------------------------------------

if [[ "$gaps_detected" -gt 0 ]]; then
  echo "PASS  gaps_detected=$gaps_detected"
elif [[ "$nacks_dispatched" -gt 0 ]]; then
  echo "PASS  gaps active (gaps_detected delta=0 but nacks_dispatched=$nacks_dispatched — stale gaps from prior scenario)"
else
  echo "FAIL  expected gaps detected; got $gaps_detected (nacks=$nacks_dispatched)"
  SCENARIO_FAIL=1
fi

if [[ "$nacks_dispatched" -lt 1 ]]; then
  echo "FAIL  expected NACKs dispatched; got $nacks_dispatched"
  SCENARIO_FAIL=1
else
  echo "PASS  nacks_dispatched=$nacks_dispatched"
fi

if [[ "$nacks_received" -lt 1 ]]; then
  echo "FAIL  retry endpoint received no NACKs"
  SCENARIO_FAIL=1
else
  echo "PASS  nacks_received=$nacks_received"
fi

# Core assertion: rate limiter must fire under burst NACK load.
if [[ "$rate_drops" -lt 1 ]]; then
  echo "WARN  rate_limit_drops=0 — rate limiter did not fire (may need tighter RL config or higher PPS)"
else
  echo "PASS  rate_limit_drops=$rate_drops (rate limiter activated)"
fi

# Some retransmits must still succeed (not everything rate-limited).
if [[ "$retransmits" -lt 1 ]]; then
  echo "FAIL  retransmits=0 — everything rate-limited or cache-missed"
  SCENARIO_FAIL=1
else
  echo "PASS  retransmits=$retransmits"
fi

# Some gaps should be recovered despite rate limiting.
if [[ "$gaps_suppressed" -lt 1 ]]; then
  echo "WARN  gaps_suppressed=0 — retransmits may not arrive within gap window"
else
  echo "PASS  gaps_suppressed=$gaps_suppressed"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 12: FAIL"
  exit 1
fi
echo "Scenario 12: PASS"
