#!/usr/bin/env bash
# Push the canonical Prometheus scrape config and Grafana listener dashboard
# onto the existing `metrics` VM (10.10.10.142). Idempotent.
#
# Prereqs:
#   - `metrics` LXD VM is RUNNING with prometheus (:9090) and grafana (:3000)
#     already installed (same instance used in previous proxy tests).
#   - docs/prometheus/prometheus.yml and docs/grafana/bitcoin-shard-listener.json
#     checked into this repo.
set -euo pipefail
exec </dev/null

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROM_FILE="$REPO_DIR/docs/prometheus/prometheus.yml"
DASH_PROXY="$REPO_DIR/docs/grafana/bitcoin-shard-proxy.json"
DASH_LISTENER="$REPO_DIR/docs/grafana/bitcoin-shard-listener.json"
DASH_RETRY="$REPO_DIR/docs/grafana/bitcoin-retry-endpoint.json"

METRICS_VM=${METRICS_VM:-metrics}
METRICS_IP=${METRICS_IP:-10.10.10.142}
GRAFANA_USER=${GRAFANA_USER:-admin}
GRAFANA_PASS=${GRAFANA_PASS:-admin}

if ! lxc info "$METRICS_VM" &>/dev/null; then
  echo "ERROR: LXD VM '$METRICS_VM' not found. Set METRICS_VM=<name> if it lives elsewhere." >&2
  exit 1
fi

echo "==> [09] Pushing Prometheus scrape config to $METRICS_VM:/etc/prometheus/prometheus.yml"
lxc exec "$METRICS_VM" -- cp /etc/prometheus/prometheus.yml "/etc/prometheus/prometheus.yml.bak.$(date +%s)" || true
lxc file push "$PROM_FILE" "$METRICS_VM/etc/prometheus/prometheus.yml"
lxc exec "$METRICS_VM" -- chown root:root /etc/prometheus/prometheus.yml
lxc exec "$METRICS_VM" -- chmod 644 /etc/prometheus/prometheus.yml

echo "==> [09] Reloading Prometheus"
if ! lxc exec "$METRICS_VM" -- systemctl reload prometheus 2>/dev/null; then
  lxc exec "$METRICS_VM" -- systemctl restart prometheus
fi

echo "==> [09] Target health:"
sleep 2
lxc exec "$METRICS_VM" -- curl -s http://localhost:9090/api/v1/targets \
  | (command -v jq >/dev/null && jq '.data.activeTargets[] | {job:.labels.job, instance:.labels.instance, health}' \
     || cat)

echo "==> [09] Importing Grafana dashboards via HTTP API"
for dash in "$DASH_PROXY" "$DASH_LISTENER" "$DASH_RETRY"; do
  [[ -f "$dash" ]] || { echo "     skipping $dash (missing)"; continue; }
  payload=$(jq -n --slurpfile d "$dash" '{dashboard: $d[0], overwrite: true, folderId: 0}')
  code=$(curl -s -o /tmp/dash.out -w '%{http_code}' \
    -u "$GRAFANA_USER:$GRAFANA_PASS" \
    -H 'Content-Type: application/json' \
    -d "$payload" \
    "http://$METRICS_IP:3000/api/dashboards/db" || echo 000)
  if [[ "$code" == "200" ]]; then
    echo "     imported $(basename "$dash")"
  else
    echo "     FAIL import $(basename "$dash") (http=$code):"
    sed 's/^/       /' /tmp/dash.out
  fi
done

echo "==> [09] Done. Grafana: http://$METRICS_IP:3000  (login: $GRAFANA_USER/$GRAFANA_PASS)"
