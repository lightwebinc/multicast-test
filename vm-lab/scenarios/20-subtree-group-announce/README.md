# Scenario 20 — BRC-127 Subtree Group Announcement Dynamic Filtering

## What it tests

End-to-end verification of the BRC-127 protocol:

1. **Proxy TCP ingress** — `shard-proxy` receives 64-byte `SubtreeGroupAnnounce`
   datagrams from the source VM over TCP and forwards them to the BRC-127 control
   multicast group (`ff05::b:fffc:9001`).

2. **Listener dynamic registry** — `shard-listener` (listener3) joins the
   control group, decodes `SubtreeGroupAnnounce` frames, and populates its in-memory
   `subtreegroup.Registry` mapping SubtreeIDs → GroupID.

3. **Group-based forwarding** — listener3 is configured with
   `-subtree-groups bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf` (no static `subtree-include`).
   After announcements arrive, all 8 subtrees are in the group → listener3 forwards
   ~100% of frames. Without BRC-127 (baseline listener3 config with `subtree-include`),
   it would only forward ~1/8 frames.

## Prerequisites

- All VMs running with BRC-127-capable binaries (see build-and-push instructions).
- Proxy deployed with `TCP_LISTEN_PORT=9002`.
- listener3 deployed with `SUBTREE_GROUPS=bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf` and no
  `SUBTREE_INCLUDE`.
- source VM has the new `subtx-gen` binary (supports `-announce-addr` and `-subtree-group`).

## Expected outcome

```
PASS  listener3 forwarded (BRC-127 group filter, expect ~100%): got N expected~N
PASS  listener3 subtree_include_miss drops within bounds: M < N/4
Scenario 20: PASS
```
