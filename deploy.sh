#!/usr/bin/env bash
# Full lab deploy: provision LXD VMs, install the proxy and listener apps,
# refresh metrics, and run the functional scenarios.
set -euo pipefail
exec </dev/null

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LAB="$SCRIPT_DIR/lab"

echo "================================================="
echo " bitcoin-multicast-test — full deployment"
echo "================================================="
echo ""

bash "$LAB/01-network.sh";  echo ""
bash "$LAB/02-profiles.sh"; echo ""
bash "$LAB/03-launch.sh";   echo ""
bash "$LAB/04-sudo.sh";     echo ""
bash "$LAB/05-packages.sh"; echo ""
bash "$LAB/06-netplan.sh";  echo ""
bash "$LAB/06b-restart.sh"; echo ""

echo "==> Enabling bridge MLD querier (required for snooping to suppress flooding)..."
if [ ! -f /etc/systemd/system/lxd-bridge-mcast-querier.service ]; then
  cat << 'EOF' | sudo tee /etc/systemd/system/lxd-bridge-mcast-querier.service > /dev/null
[Unit]
Description=Enable MLD querier on lxdbr1 for multicast snooping
After=sys-devices-virtual-net-lxdbr1.device
BindsTo=sys-devices-virtual-net-lxdbr1.device

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'echo 1 > /sys/devices/virtual/net/lxdbr1/bridge/multicast_querier'

[Install]
WantedBy=multi-user.target
EOF
  sudo systemctl daemon-reload
fi
sudo systemctl enable --now lxd-bridge-mcast-querier.service
echo ""

echo "==> Deploying Ansible playbooks (proxy + listeners)"
bash "$SCRIPT_DIR/ansible/run-deploy.sh"
echo ""

echo "==> Building and installing source VM binaries"
(
  SUBTX_ROOT="$(dirname "$SCRIPT_DIR")/bitcoin-subtx-generator"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o /tmp/subtx-gen            "$SUBTX_ROOT/cmd/subtx-gen"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o /tmp/send-block-announce  "$SUBTX_ROOT/cmd/send-block-announce"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o /tmp/send-subtree-data    "$SUBTX_ROOT/cmd/send-subtree-data"
  for bin in subtx-gen send-block-announce send-subtree-data; do
    lxc file push /tmp/$bin source/usr/local/bin/$bin
    lxc exec source -- chmod +x /usr/local/bin/$bin
    echo "    ok  source:/usr/local/bin/$bin"
  done
)
echo ""

bash "$LAB/07-firewall-verify.sh"; echo ""
bash "$LAB/08-verify.sh";          echo ""
bash "$LAB/09-metrics-update.sh";  echo ""

echo "================================================="
echo " Deployment complete."
echo ""
echo " Next steps:"
echo "   bash scenarios/00-firewall/run.sh"
echo "   bash scenarios/01-functional-all-shards/run.sh"
echo "   bash scenarios/02-functional-shard-filter/run.sh"
echo "   bash scenarios/03-functional-subtree-filter/run.sh"
echo ""
echo " Grafana: http://10.10.10.142:3000  (admin/admin)"
echo "================================================="
