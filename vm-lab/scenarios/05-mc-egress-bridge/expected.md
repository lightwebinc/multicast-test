# Scenario 05 — Expected Results

## listener1 (sender / re-emitter)

| Metric | Expected | Tolerance |
|---|---|---|
| `bsl_frames_forwarded_total{proto="udp-mcast"}` Δ | ≈ frames sent | 5% |
| `bsl_mc_egress_errors_total` Δ | 0 | hard fail |

The `udp-mcast` forwarded count must be within 5% of the generator's actual
sent count. Any non-zero `bsl_mc_egress_errors_total` is a hard failure —
errors indicate socket send failures (e.g. nft OUTPUT rule missing, wrong
interface, or send buffer overflow).

## listener4 (downstream receiver on ff02:: domain)

| Metric | Expected | Tolerance |
|---|---|---|
| `bsl_frames_received_total` Δ | ≈ frames sent | 10% |
| `bsl_frames_forwarded_total` Δ | ≈ frames sent | 10% |

listener4 has no shard or subtree filter, so it should receive and forward all
frames re-emitted by listener1. The 10% tolerance accommodates MLD snooping
convergence time and bridge delivery jitter on lxdbr1.

## Notes

- listener1's unicast egress (`bsl_frames_forwarded_total{proto="udp"}`) must
  also be ≈ frames sent — the mc-egress path is additive, not a replacement.
- `bsl_frames_received_total` on listener4 may briefly lag due to MLD join
  latency; the 5s pre-generator wait mitigates this.
