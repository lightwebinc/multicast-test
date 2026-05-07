#!/usr/bin/env bash
set -euo pipefail
exec </dev/null

echo "==> [01] Configuring lxdbr0 DHCP range..."
lxc network set lxdbr0 ipv4.dhcp.ranges=10.10.10.100-10.10.10.200

echo "==> [01] Creating lxdbr1 (IPv6-only egress/multicast bridge)..."
if lxc network show lxdbr1 &>/dev/null; then
  echo "     lxdbr1 already exists, skipping create"
else
  lxc network create lxdbr1 \
    ipv4.address=none \
    ipv4.nat=false \
    ipv6.address=fd20::1/64 \
    ipv6.nat=false \
    bridge.mtu=1500
fi

echo "==> [01] Enabling multicast snooping + querier on lxdbr1..."
echo 1 | sudo tee /sys/devices/virtual/net/lxdbr1/bridge/multicast_snooping > /dev/null
echo 1 | sudo tee /sys/devices/virtual/net/lxdbr1/bridge/multicast_querier > /dev/null

echo "==> [01] Tuning lxdbr1 bridge performance..."
sudo ip link set lxdbr1 txqueuelen 10000

echo "==> [01] Done."
