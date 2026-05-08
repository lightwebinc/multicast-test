#!/usr/bin/env bash
# Wipe Prometheus TSDB storage on the metrics VM for a clean Grafana slate.
# Stops prometheus, removes the data directory, restarts it.
#
# Usage:
#   bash lab/reset-prometheus.sh
#   METRICS_VM=metrics PROM_DATA=/var/lib/prometheus/metrics2 bash lab/reset-prometheus.sh
set -euo pipefail

METRICS_VM=${METRICS_VM:-metrics}
PROM_DATA=${PROM_DATA:-/var/lib/prometheus/metrics2}

echo "==> Stopping prometheus on $METRICS_VM"
lxc exec "$METRICS_VM" -- systemctl stop prometheus

echo "==> Wiping TSDB data ($PROM_DATA)"
lxc exec "$METRICS_VM" -- rm -rf "$PROM_DATA"
lxc exec "$METRICS_VM" -- mkdir -p "$PROM_DATA"
lxc exec "$METRICS_VM" -- chown prometheus:prometheus "$PROM_DATA"

echo "==> Starting prometheus"
lxc exec "$METRICS_VM" -- systemctl start prometheus

echo "==> Waiting for prometheus to come up"
for i in $(seq 1 15); do
  if lxc exec "$METRICS_VM" -- curl -sf http://localhost:9090/-/ready &>/dev/null; then
    echo "    ready"
    break
  fi
  sleep 1
done

echo "==> Done. Grafana will show clean data from the next scrape (~15s)."
