#!/usr/bin/env bash
# Install FRR on BGP router VMs and enable bgpd + zebra.
set -euo pipefail
exec </dev/null

BGP_VMS=(router1 router2)

echo "==> [05b] Installing FRR on BGP router VMs..."

for vm in "${BGP_VMS[@]}"; do
  echo "     $vm: apt-get update + install frr..."
  lxc exec "$vm" -- bash -c "export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y frr"

  echo "     $vm: enabling bgpd and zebra..."
  lxc exec "$vm" -- sed -i 's/^bgpd=no/bgpd=yes/' /etc/frr/daemons
  lxc exec "$vm" -- sed -i 's/^zebra=no/zebra=yes/' /etc/frr/daemons

  echo "     $vm: enabling and starting frr.service..."
  lxc exec "$vm" -- systemctl enable frr
  lxc exec "$vm" -- systemctl restart frr
done

echo "==> [05b] Done."
