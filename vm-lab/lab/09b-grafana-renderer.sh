#!/usr/bin/env bash
# Install the Grafana image renderer plugin on the `metrics` VM.
# Idempotent — safe to re-run.
#
# Prerequisites:
#   - `metrics` LXD VM is RUNNING with Grafana already installed (:3000).
set -euo pipefail
exec </dev/null

METRICS_VM=${METRICS_VM:-metrics}
METRICS_IP=${METRICS_IP:-10.10.10.142}
GRAFANA_USER=${GRAFANA_USER:-admin}
GRAFANA_PASS=${GRAFANA_PASS:-admin}

if ! lxc info "$METRICS_VM" &>/dev/null; then
  echo "ERROR: LXD VM '$METRICS_VM' not found. Set METRICS_VM=<name> if it lives elsewhere." >&2
  exit 1
fi

# System libraries required by the renderer's bundled Chromium on Ubuntu 24.04.
CHROME_DEPS=(
  libx11-6 libxcomposite1 libxdamage1 libxext6 libxfixes3 libxrandr2
  libgbm1 libxkbcommon0 libasound2t64
  libatk1.0-0 libatk-bridge2.0-0
  libcairo2 libcups2t64 libdbus-1-3 libdrm2
  libnss3 libpango-1.0-0 libpangocairo-1.0-0
  libxshmfence1 libxss1 libxtst6
  fonts-liberation
)

echo "==> [09b] Installing Chromium system dependencies on $METRICS_VM..."
lxc exec "$METRICS_VM" -- bash -c \
  "export DEBIAN_FRONTEND=noninteractive; \
   apt-get update -qq && \
   apt-get install -y -o Dpkg::Options::=--force-confnew ${CHROME_DEPS[*]}"

# v4.1.x introduced a strict path-traversal check that rejects the absolute
# temp-file path Grafana 12 sends to the renderer ("File path should not
# include directories").  v3.11.6 does not have this check and renders
# correctly with Grafana 12.x.
RENDERER_VERSION=3.11.6

echo "==> [09b] Installing grafana-image-renderer plugin (v${RENDERER_VERSION})..."
installed_ver=$(lxc exec "$METRICS_VM" -- grafana-cli plugins ls 2>/dev/null \
  | grep grafana-image-renderer | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || true)
if [[ "$installed_ver" == "$RENDERER_VERSION" ]]; then
  echo "     grafana-image-renderer $RENDERER_VERSION already installed, skipping"
else
  lxc exec "$METRICS_VM" -- grafana-cli plugins install grafana-image-renderer "$RENDERER_VERSION"
fi

# Grafana 12 rejects the renderer with "modified signature" (grafana-cli unzips
# the archive which alters file metadata vs what the MANIFEST.txt was signed
# against).  Removing MANIFEST.txt downgrades the state to "unsigned", which
# allow_loading_unsigned_plugins covers.
echo "==> [09b] Removing MANIFEST.txt so plugin loads as 'unsigned' (not 'modified-signature')..."
lxc exec "$METRICS_VM" -- rm -f /var/lib/grafana/plugins/grafana-image-renderer/MANIFEST.txt

# Allow unsigned renderer plugin in grafana.ini (idempotent).
echo "==> [09b] Patching grafana.ini: allow_loading_unsigned_plugins..."
lxc exec "$METRICS_VM" -- bash -c "
  if grep -q '^allow_loading_unsigned_plugins' /etc/grafana/grafana.ini; then
    # already set — ensure renderer is in the list
    if ! grep '^allow_loading_unsigned_plugins' /etc/grafana/grafana.ini | grep -q grafana-image-renderer; then
      sed -i 's/^allow_loading_unsigned_plugins = /allow_loading_unsigned_plugins = grafana-image-renderer,/' /etc/grafana/grafana.ini
    fi
  else
    # replace the commented-out stub line
    sed -i 's/^;allow_loading_unsigned_plugins =.*/allow_loading_unsigned_plugins = grafana-image-renderer/' /etc/grafana/grafana.ini
  fi
"

echo "==> [09b] Restarting grafana-server..."
lxc exec "$METRICS_VM" -- systemctl restart grafana-server

echo "==> [09b] Waiting for Grafana to come up..."
for i in $(seq 1 20); do
  if lxc exec "$METRICS_VM" -- curl -sf "http://localhost:3000/api/health" &>/dev/null; then
    break
  fi
  sleep 2
done

echo "==> [09b] Renderer plugin health check:"
lxc exec "$METRICS_VM" -- curl -s \
  -u "$GRAFANA_USER:$GRAFANA_PASS" \
  "http://localhost:3000/api/plugins/grafana-image-renderer/health" \
  | (command -v jq &>/dev/null && jq . || cat)

echo ""
echo "==> [09b] Done. Grafana: http://$METRICS_IP:3000"
