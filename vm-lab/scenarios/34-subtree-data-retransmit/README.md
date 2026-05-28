# Scenario 34 — BRC-132 subtree data: NACK retransmission

Injects packet loss on listeners while sending inline BRC-132 SubtreeData
frames. Retry endpoints join `GroupSubtreeAnnounce` (FF0X::B:FFFB) and
cache V5 frames by `HashKey ∥ SeqNum`. Listeners detect SeqNum gaps on the
`brc132` flow and dispatch NACKs. Retry endpoints retransmit back to
`GroupSubtreeAnnounce`.

## Expectations

| Metric                                        | Condition                      |
| --------------------------------------------- | ------------------------------ |
| `bsl_gaps_detected_total{flow="brc132"}`      | > 0 (loss produces gaps)       |
| `bsl_nacks_dispatched_total{flow="brc132"}`   | > 0                            |
| `bsl_gaps_unrecovered_total{flow="brc132"}`   | ≈ 0 (retransmit fills them)    |
| `bsl_frames_received_total{version="brc132"}` | ≈ FRAME_COUNT on each listener |
| `bre_retransmits_total` (retry endpoints)     | > 0                            |

## Prerequisites

- `SUBTREE_DATA_ENABLED=true` in listener config.env.
- `SUBTREE_DATA_ENABLED=true` in retry endpoint config.env.
- Proxy TCP ingress enabled.
- At least one retry endpoint reachable.
