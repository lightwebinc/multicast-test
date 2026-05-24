# Scenario 33 — BRC-132 subtree data: fragmented delivery

Sends BRC-132 SubtreeData frames with a payload large enough to exceed the
proxy's `FRAG_MTU` (default 1500 bytes). The proxy fragments each frame into
BRC-130 datagrams with `OrigFrameVer=0x05`. Listeners reassemble the fragments
and deliver via the `SubtreeDataCallback`, then forward to egress.

At `FRAG_MTU=1500` and `PAYLOAD_SIZE=8192`:
- fragDataSize = 1500 − 40 (IPv6) − 8 (UDP) − 104 (BRC-130 header) = 1348 B
- Fragments per frame = ceil(8192/1348) = 7

## Expectations

| Metric | Condition |
|--------|-----------|
| `bsl_reassembly_started_total` | ≈ FRAME_COUNT (one slot per SubtreeID/TxID) |
| `bsl_reassembly_completed_total` | ≈ started (all fragments arrive; no loss) |
| `bsl_reassembly_abandoned_total` | == 0 |
| `bsl_frames_received_total{version="brc132_reassembled"}` | ≈ FRAME_COUNT |
| `bsl_frames_forwarded_total{proto="udp"}` | ≥ completed |

## Prerequisites

- `SUBTREE_DATA_ENABLED=true` in listener config.env.
- Proxy TCP ingress enabled (`TCP_LISTEN_PORT=9002`).
- Proxy `FRAG_MTU` set (script enables it automatically).
