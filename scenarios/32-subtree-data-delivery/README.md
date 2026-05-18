# Scenario 32 — BRC-132 subtree data: basic delivery

Sends BRC-132 SubtreeData frames (hashes-only, inline payload ≤ MTU) via
TCP to the proxy. The proxy stamps HashKey/SeqNum and forwards frames to
`CtrlGroupSubtreeAnnounce` (FF0X::B:FFFB). All listeners that have
`SUBTREE_DATA_ENABLED=true` join this group and must receive and forward
every frame.

## Expectations

| Metric | Condition |
|--------|-----------|
| `bsl_frames_received_total{version="brc132"}` | ≈ FRAME_COUNT on every enabled listener |
| `bsl_frames_forwarded_total{proto="udp"}` | ≥ received |
| `bsl_gaps_detected_total{flow="brc132"}` | == 0 (no loss injected) |
| `bsl_reassembly_started_total` | == 0 (payload fits in one datagram) |

## Prerequisites

- `SUBTREE_DATA_ENABLED=true` in listener config.env on all listener VMs.
- Proxy TCP ingress enabled (`TCP_LISTEN_PORT=9002`).
- All services running with current binaries.
