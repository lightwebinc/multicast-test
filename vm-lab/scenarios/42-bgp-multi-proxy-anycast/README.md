# Scenario 42 — BGP Multi-Proxy AnyCast (Future)

## Purpose

Verify ECMP behavior when two proxy VMs both announce the same AnyCast VIP
(`192.0.2.0/24` + `2001:db8:ffff::/48`) via iBGP to router2.

## Prerequisites

- Second proxy VM (`proxy2`) added to lxdbr3 at 198.51.100.19/28
- proxy2 iBGP peering with router2
- Both proxies announcing the same prefixes

## Expected

- router2 installs two equal-cost paths (ECMP) for the AnyCast prefix
- router1 sees a single best path from router2 (or ECMP if add-path is enabled)
- Withdrawing one proxy leaves the other path active
