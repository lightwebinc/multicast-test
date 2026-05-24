#!/usr/bin/env bash
set -euo pipefail
exec </dev/null

echo "==> [02] Creating LXD profile: ubuntu-small-mcast (2 NICs)..."
if lxc profile show ubuntu-small-mcast &>/dev/null; then
  echo "     ubuntu-small-mcast already exists, skipping"
else
  lxc profile create ubuntu-small-mcast
  lxc profile set ubuntu-small-mcast limits.cpu=2
  lxc profile set ubuntu-small-mcast limits.memory=2GiB

  lxc profile device add ubuntu-small-mcast eth0 nic network=lxdbr0 name=eth0
  lxc profile device add ubuntu-small-mcast eth1 nic network=lxdbr1 name=eth1
  lxc profile device add ubuntu-small-mcast root disk path=/ pool=vmpool size=15GiB
fi

echo "==> [02] Creating LXD profile: ubuntu-small-single (1 NIC)..."
if lxc profile show ubuntu-small-single &>/dev/null; then
  echo "     ubuntu-small-single already exists, skipping"
else
  lxc profile create ubuntu-small-single
  lxc profile set ubuntu-small-single limits.cpu=2
  lxc profile set ubuntu-small-single limits.memory=2GiB

  lxc profile device add ubuntu-small-single eth0 nic network=lxdbr0 name=eth0
  lxc profile device add ubuntu-small-single root disk path=/ pool=vmpool size=15GiB
fi

echo "==> [02] Creating LXD profile: ubuntu-source (mgmt + lxdbr4 source LAN)..."
if lxc profile show ubuntu-source &>/dev/null; then
  echo "     ubuntu-source already exists, skipping"
else
  lxc profile create ubuntu-source
  lxc profile set ubuntu-source limits.cpu=2
  lxc profile set ubuntu-source limits.memory=2GiB

  lxc profile device add ubuntu-source eth0 nic network=lxdbr0 name=eth0
  lxc profile device add ubuntu-source eth1 nic network=lxdbr4 name=eth1
  lxc profile device add ubuntu-source root disk path=/ pool=vmpool size=15GiB
fi

echo "==> [02] Creating LXD profile: ubuntu-bgp-r1 (mgmt + lxdbr2 + lxdbr4)..."
if lxc profile show ubuntu-bgp-r1 &>/dev/null; then
  echo "     ubuntu-bgp-r1 already exists, skipping"
else
  lxc profile create ubuntu-bgp-r1
  lxc profile set ubuntu-bgp-r1 limits.cpu=2
  lxc profile set ubuntu-bgp-r1 limits.memory=2GiB

  lxc profile device add ubuntu-bgp-r1 eth0 nic network=lxdbr0 name=eth0
  lxc profile device add ubuntu-bgp-r1 eth1 nic network=lxdbr2 name=eth1
  lxc profile device add ubuntu-bgp-r1 eth2 nic network=lxdbr4 name=eth2
  lxc profile device add ubuntu-bgp-r1 root disk path=/ pool=vmpool size=15GiB
fi

echo "==> [02] Creating LXD profile: ubuntu-bgp-r2 (mgmt + lxdbr2 + lxdbr3)..."
if lxc profile show ubuntu-bgp-r2 &>/dev/null; then
  echo "     ubuntu-bgp-r2 already exists, skipping"
else
  lxc profile create ubuntu-bgp-r2
  lxc profile set ubuntu-bgp-r2 limits.cpu=2
  lxc profile set ubuntu-bgp-r2 limits.memory=2GiB

  lxc profile device add ubuntu-bgp-r2 eth0 nic network=lxdbr0 name=eth0
  lxc profile device add ubuntu-bgp-r2 eth1 nic network=lxdbr2 name=eth1
  lxc profile device add ubuntu-bgp-r2 eth2 nic network=lxdbr3 name=eth2
  lxc profile device add ubuntu-bgp-r2 root disk path=/ pool=vmpool size=15GiB
fi

echo "==> [02] Done."
