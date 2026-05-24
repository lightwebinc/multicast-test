# Scenario 41 — BGP Ingress Failover

## Purpose

Verify that when the proxy's health check fails, it withdraws its AnyCast VIP
from BGP, and the withdrawal propagates through router2 to router1.

## Prerequisites

- Scenario 40 passing (routes present in all RIBs)
- Proxy health-check script accessible

## Expected

- After stopping the proxy health-check (or the proxy service), the VIP
  disappears from router2 and router1 RIBs within hold-time.
- After restarting, the VIP re-appears.
