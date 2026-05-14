# Scenario 05 — Multicast Egress Bridge (Group Re-mapping)

listener1 receives frames on the ingress domain (`ff05::b:0-3`, site-local) and
re-emits them onto a remapped egress domain (`ff02::0-3`, link-local) using
`-mc-egress-enabled -mc-egress-scope=link`. listener4 joins only the link-local
groups and acts as the terminal downstream consumer.

This verifies `MCastSender`'s per-frame group address re-write (scope prefix
`ff05` → `ff02`, same shard index and port) and end-to-end delivery across the
domain boundary.

## Prerequisites

- listener1 deployed with `mc_egress_enabled=true`, `mc_egress_scope=link`
  (see `ansible/listener-hosts.yml`)
- listener4 deployed with `mc_scope=link`, `beacon_enabled=false`
  (new VM: 10.10.10.37 / fd20::27)
- New binary (`bsl-new`) pushed to all four listener VMs
- listener1 nft OUTPUT chain includes the mc-egress rule
  (`bitcoin-listener.nft.j2` rendered with `mc_egress_enabled=true`)

## Expected

| Listener | Metric | Expected Δ |
|---|---|---|
| listener1 | `bsl_frames_forwarded_total{proto="udp-mcast"}` | ≈ frames sent (tol 5%) |
| listener1 | `bsl_mc_egress_errors_total` | 0 |
| listener4 | `bsl_frames_received_total` | ≈ frames sent (tol 10%) |
| listener4 | `bsl_frames_forwarded_total` | ≈ frames sent (tol 10%) |

listener4 tolerance is 10% (vs 5% for listener1) because receipt depends on
MLD snooping convergence on lxdbr1. A brief wait after service start ensures
the bridge MDB is populated before traffic begins.

## Run

```bash
bash run.sh
```
