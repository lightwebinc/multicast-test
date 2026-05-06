#!/usr/bin/env bash
set -euo pipefail
exec </dev/null

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NETPLAN_DIR="$SCRIPT_DIR/06-netplan"

echo "==> [06] Pushing netplan configs and applying static IPs..."

for vm in source proxy listener1 listener2 listener3 listener4 retry1 retry2 retry3; do
  echo "     $vm: pushing 99-lab.yaml..."
  lxc file push "$NETPLAN_DIR/$vm.yaml" "$vm/etc/netplan/99-lab.yaml"
  lxc exec "$vm" -- chmod 600 /etc/netplan/99-lab.yaml
  echo "     $vm: applying netplan..."
  lxc exec "$vm" -- netplan apply
done

echo "==> [06] Done."
