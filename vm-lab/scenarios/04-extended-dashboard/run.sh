#!/usr/bin/env bash
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Set extended duration BEFORE sourcing common.sh (which defaults to 10s)
: "${DURATION:=24h}"

source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Running extended test: PPS=$PPS, DURATION=$DURATION"

frames=$(run_generator)

echo "==> Allow egress pipeline to drain"
sleep 5

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

echo "==> Summary"
echo "Frames sent: $frames"
echo "Duration: $DURATION"
echo ""

# Show deltas for all listeners without strict assertions
for i in "${!LISTENERS[@]}"; do
  host="${LISTENERS[$i]}"
  echo "--- $host ---"
  received=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_frames_received_total)
  forwarded=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_frames_forwarded_total)
  dropped_shard=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_dropped_total|shard_filter')
  dropped_subtree=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_dropped_total|subtree_exclude')
  dropped_include=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_dropped_total|subtree_include_miss')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_gaps_detected_total)
  nacks=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_nacks_dispatched_total)

  echo "  Received:    $received"
  echo "  Forwarded:    $forwarded"
  echo "  Dropped (shard): $dropped_shard"
  echo "  Dropped (subtree): $dropped_subtree"
  echo "  Dropped (include miss): $dropped_include"
  echo "  Gaps:        $gaps"
  echo "  NACKs:       $nacks"
  echo ""
done

echo "==> Dashboard population complete"
echo "Check Grafana for visualization"
