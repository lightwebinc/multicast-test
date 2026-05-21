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

echo "==> [01] Creating lxdbr2 (p2p eBGP link: router1 <-> router2)..."
if lxc network show lxdbr2 &>/dev/null; then
  echo "     lxdbr2 already exists, skipping create"
else
  lxc network create lxdbr2 \
    ipv4.address=203.0.113.3/30 \
    ipv4.nat=false \
    ipv6.address=2001:db8:b::ffff/64 \
    ipv6.nat=false \
    bridge.mtu=1500
fi

echo "==> [01] Creating lxdbr3 (iBGP peering LAN: router2 + proxies)..."
if lxc network show lxdbr3 &>/dev/null; then
  echo "     lxdbr3 already exists, skipping create"
else
  lxc network create lxdbr3 \
    ipv4.address=198.51.100.30/28 \
    ipv4.nat=false \
    ipv6.address=2001:db8:d::ffff/64 \
    ipv6.nat=false \
    bridge.mtu=1500
fi

echo "==> [01] Enabling multicast snooping + querier on lxdbr1..."
echo 1 | sudo tee /sys/devices/virtual/net/lxdbr1/bridge/multicast_snooping > /dev/null
echo 1 | sudo tee /sys/devices/virtual/net/lxdbr1/bridge/multicast_querier > /dev/null

echo "==> [01] Tuning lxdbr1 bridge performance..."
sudo ip link set lxdbr1 txqueuelen 10000

echo "==> [01] Done."
