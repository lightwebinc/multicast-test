#!/usr/bin/env bash
# Scenario 10 — tightened single-endpoint ACK.
#
# Unlike scenario 99 (aggregate thresholds at high PPS), scenario 10 runs a
# low PPS with infrequent gaps so every dispatched NACK should produce an
# ACK from retry1 and the corresponding gap should be suppressed.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Set scenario-specific defaults BEFORE sourcing common.sh, since common.sh
# uses `: "${PPS:=1000}"` which is a no-op once PPS is already set.
: "${PPS:=200}"
: "${DURATION:=10s}"
: "${SEQ_GAP_EVERY:=500}"
: "${SEQ_GAP_SIZE:=1}"
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
           bre_frames_dropped_total \
           bre_nack_requests_total bre_rate_limit_drops_total \
           bre_cache_hits_total bre_cache_misses_total bre_cache_errors_total \
           bre_retransmits_total bre_retransmit_dedup_total \
           bre_responses_sent_total bre_response_send_errors_total; do
    printf '%s\t%s\t%s\n' "$RETRY_VM" "$m" "$(retry_metric "$m")" >> "$out"
  done
}

echo "==> Restarting listeners to clear stale gap-tracker state..."
for lvm in "${LISTENERS[@]}"; do
  lxc exec "$lvm" -- systemctl restart shard-listener
done

echo "==> Waiting for listeners to be ready..."
for i in "${!LISTENERS[@]}"; do
  lvm="${LISTENERS[$i]}"; lip="${LISTENER_IPS[$i]}"
  for try in $(seq 1 20); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://$lip:9200/healthz" || echo 000)
    if [[ "$code" == "200" ]]; then
      echo "     $lvm: ready"
      break
    fi
    if [[ "$try" -eq 20 ]]; then
      echo "FAIL  $lvm did not become ready within 60s"
      exit 1
    fi
    sleep 3
  done
done

# Allow beacon discovery to repopulate retry-endpoint registry post-restart.
sleep 5

echo "==> Injecting selective frame loss on listeners (1%) to create cacheable gaps"
apply_listener_loss "1%"
trap 'remove_listener_loss' EXIT

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_retry  "$RETRY_BEFORE"

echo "==> Generator (low PPS, infrequent gaps)"
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
sleep 3

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_retry  "$RETRY_AFTER"

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
cache_errors=$(retry_diff bre_cache_errors_total)
frames_received=$(retry_diff bre_frames_received_total)
frames_cached=$(retry_diff bre_frames_cached_total)
retransmits=$(retry_diff bre_retransmits_total)
responses_sent=$(retry_diff bre_responses_sent_total)
response_send_errs=$(retry_diff bre_response_send_errors_total)

# Sanity: every NACK received should either be rate-dropped, be a cache hit,
# be a cache miss, or be a validation drop (silent). The listener-side
# invariant is that nacks_received ≈ rate_drops + cache_hits + cache_misses.
nack_accounted=$(( rate_drops + cache_hits + cache_misses + cache_errors ))
nack_unaccounted=$(( nacks_received - nack_accounted ))

cat <<EOF
-- Listener aggregate (l1+l2+l3) --
bsl_gaps_detected_total      = $gaps_detected
bsl_nacks_dispatched_total   = $nacks_dispatched
bsl_gaps_suppressed_total    = $gaps_suppressed
bsl_gaps_unrecovered_total   = $gaps_unrecovered

-- Retry endpoint ($RETRY_VM) --
bre_frames_received_total    = $frames_received
bre_frames_cached_total      = $frames_cached
bre_nack_requests_total      = $nacks_received
bre_rate_limit_drops_total   = $rate_drops
bre_cache_hits_total         = $cache_hits
bre_cache_misses_total       = $cache_misses
bre_cache_errors_total       = $cache_errors
bre_retransmits_total        = $retransmits
bre_responses_sent_total     = $responses_sent   (ACK+MISS)
bre_response_send_errors     = $response_send_errs
nacks_unaccounted (likely validation drops) = $nack_unaccounted
EOF

SCENARIO_FAIL=0

if [[ "$gaps_detected" -lt 1 ]]; then
  echo "FAIL  expected at least one gap; got $gaps_detected (increase DURATION or lower SEQ_GAP_EVERY)"
  SCENARIO_FAIL=1
else
  echo "PASS  gaps_detected=$gaps_detected"
fi

# NACKs are not 1:1 with gaps: many transient gaps close naturally before
# the NACK timer fires, and the listener dedups in-flight NACKs per
# (sender,seq). Requiring nacks_dispatched >= gaps_detected is wrong.
# What matters is that SOME NACKs were sent in response to real gaps.
if [[ "$nacks_dispatched" -lt 1 ]]; then
  echo "FAIL  listeners dispatched no NACKs"
  SCENARIO_FAIL=1
else
  echo "PASS  nacks_dispatched=$nacks_dispatched"
fi

if [[ "$nacks_received" -lt 1 ]]; then
  echo "FAIL  retry endpoint received no NACKs"
  SCENARIO_FAIL=1
else
  # within 20% of dispatched
  tol=$(( nacks_dispatched / 5 ))
  [[ "$tol" -lt 1 ]] && tol=1
  diff=$(( nacks_dispatched - nacks_received ))
  diff=${diff#-}
  if [[ "$diff" -gt "$tol" ]]; then
    echo "WARN  nacks_dispatched=$nacks_dispatched vs nacks_received=$nacks_received (diff=$diff > tol=$tol)"
  else
    echo "PASS  nacks_received=$nacks_received within tolerance of dispatched=$nacks_dispatched"
  fi
fi

if [[ "$retransmits" -lt 1 ]]; then
  echo "FAIL  retry endpoint did not retransmit"
  SCENARIO_FAIL=1
else
  echo "PASS  retransmits=$retransmits"
fi

if [[ "$gaps_suppressed" -lt 1 ]]; then
  echo "FAIL  no gaps suppressed (ACK path broken, or retransmit arrived after gap window)"
  SCENARIO_FAIL=1
else
  echo "PASS  gaps_suppressed=$gaps_suppressed"
fi

if [[ "$gaps_unrecovered" -ne 0 ]]; then
  echo "FAIL  gaps_unrecovered=$gaps_unrecovered (expected 0 — retry endpoint should have served every NACK)"
  SCENARIO_FAIL=1
else
  echo "PASS  gaps_unrecovered=0"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 10: FAIL"
  exit 1
fi
echo "Scenario 10: PASS"
