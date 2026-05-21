# Scenario 40 — BGP Ingress Announce

## Purpose

Verify that the proxy announces its AnyCast VIP (`192.0.2.0/24` + `2001:db8:ffff::/48`)
via iBGP to router2, and that router2 re-advertises it via eBGP to router1.

## Prerequisites

- BGP lab VMs running (`LAUNCH_BGP=1`)
- FRR configured on router1 and router2
- Proxy BGP enabled with BIRD2

## Expected

- `router2` RIB contains `192.0.2.0/24` and `2001:db8:ffff::/48` via iBGP from proxy
- `router1` RIB contains `192.0.2.0/24` and `2001:db8:ffff::/48` via eBGP from router2
- AS path on router1: `65001`
