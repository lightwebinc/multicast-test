# Scenario 36 — BRC-134 anchor transaction: basic delivery

Sends BRC-134 anchor frames (FrameVerV6 = 0x06) via UDP to the proxy. The
proxy stamps HashKey and SeqNum in-place and forwards each frame to
`FF0E::B:FFFE` (CtrlGroupControl). All listeners subscribe to this group and
must receive every anchor frame regardless of their shard or subtree filter
configuration.

## What it tests

- The UDP dispatch path recognises FrameVerV6 and calls `ProcessAnchor`.
- Anchor frames bypass shard and subtree filtering on the listener side.
- `bsl_frames_received_total{version="brc134"}` increments on every listener.

## Pass criteria

| Metric                                          | Expected         |
| ----------------------------------------------- | ---------------- |
| `bsl_frames_received_total{version="brc134"}`   | == ANCHOR\_COUNT |
| `bsl_frames_forwarded_total{proto="udp"}`       | >= brc134 count  |
| `bsl_gaps_detected_total{flow="brc134"}`        | == 0             |

## Run

```bash
bash scenarios/36-anchor-delivery/run.sh
ANCHOR_COUNT=50 bash scenarios/36-anchor-delivery/run.sh
```
