#!/usr/bin/env bash
# Scenario 40: BGP Ingress Announce — verify AnyCast prefix propagation
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/common.sh
source "$SCRIPT_DIR/../lib/common.sh"

echo "=== Scenario 40: BGP Ingress Announce ==="

echo "--- Checking router2 BGP RIB (IPv4) ---"
R2_V4=$(lxc exec router2 -- vtysh -c 'show bgp ipv4 unicast 192.0.2.0/24' 2>&1)
if echo "$R2_V4" | grep -q "192.0.2.0/24"; then
  echo "PASS: router2 has 192.0.2.0/24 in IPv4 RIB"
else
  echo "FAIL: router2 missing 192.0.2.0/24"
  echo "$R2_V4"
  exit 1
fi

echo "--- Checking router2 BGP RIB (IPv6) ---"
R2_V6=$(lxc exec router2 -- vtysh -c 'show bgp ipv6 unicast 2001:db8:ffff::/48' 2>&1)
if echo "$R2_V6" | grep -q "2001:db8:ffff::/48"; then
  echo "PASS: router2 has 2001:db8:ffff::/48 in IPv6 RIB"
else
  echo "FAIL: router2 missing 2001:db8:ffff::/48"
  echo "$R2_V6"
  exit 1
fi

echo "--- Checking router1 BGP RIB (IPv4) ---"
R1_V4=$(lxc exec router1 -- vtysh -c 'show bgp ipv4 unicast 192.0.2.0/24' 2>&1)
if echo "$R1_V4" | grep -q "192.0.2.0/24"; then
  echo "PASS: router1 has 192.0.2.0/24 in IPv4 RIB"
else
  echo "FAIL: router1 missing 192.0.2.0/24"
  echo "$R1_V4"
  exit 1
fi

echo "--- Checking router1 BGP RIB (IPv6) ---"
R1_V6=$(lxc exec router1 -- vtysh -c 'show bgp ipv6 unicast 2001:db8:ffff::/48' 2>&1)
if echo "$R1_V6" | grep -q "2001:db8:ffff::/48"; then
  echo "PASS: router1 has 2001:db8:ffff::/48 in IPv6 RIB"
else
  echo "FAIL: router1 missing 2001:db8:ffff::/48"
  echo "$R1_V6"
  exit 1
fi

echo "--- Verifying AS path on router1 ---"
if echo "$R1_V4" | grep -q "65001"; then
  echo "PASS: AS path includes 65001"
else
  echo "FAIL: AS path missing 65001"
  exit 1
fi

echo ""
echo "=== Scenario 40: ALL CHECKS PASSED ==="
