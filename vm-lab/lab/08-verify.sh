#!/usr/bin/env bash
set -euo pipefail
exec </dev/null

echo "==> [08] Verification checks..."

echo ""
echo "--- Bridge multicast DB ---"
bridge mdb show dev lxdbr1

echo ""
echo "--- Bridge MLD snooping + querier state ---"
printf 'multicast_snooping = '; cat /sys/devices/virtual/net/lxdbr1/bridge/multicast_snooping
printf 'multicast_querier  = '; cat /sys/devices/virtual/net/lxdbr1/bridge/multicast_querier
echo "  (both must be 1 — querier is required for snooping to suppress flooding)"
systemctl is-active lxd-bridge-mcast-querier.service && echo "  lxd-bridge-mcast-querier.service: active" \
  || echo "  WARNING: lxd-bridge-mcast-querier.service is not active — querier will not survive reboot"

echo ""
echo "--- MLD group membership per listener ---"
for vm in listener1 listener2 listener3 listener4; do
  echo ""
  echo "  [$vm] ip maddr show dev enp6s0:"
  lxc exec "$vm" -- ip maddr show dev enp6s0
  echo "  [$vm] ip -6 addr show enp6s0:"
  lxc exec "$vm" -- ip -6 addr show enp6s0
  echo "  [$vm] bitcoin-shard-listener service state:"
  lxc exec "$vm" -- systemctl is-active bitcoin-shard-listener.service || true
done

echo ""
echo "--- IPv6 addr on proxy enp6s0 ---"
lxc exec proxy -- ip -6 addr show enp6s0

echo ""
echo "==> [08] Done."
