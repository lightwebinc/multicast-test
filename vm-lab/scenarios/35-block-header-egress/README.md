# Scenario 35 — Block Header Egress

## Goal

Verify that a listener configured with `HEADER_EGRESS_ENABLED=true` extracts
80-byte block headers from BRC-131 BlockAnnounce frames and forwards them as
stripped 172-byte BRC-131 datagrams (92B header + 80B payload) to a downstream
unicast sink. CoinbaseTx frames must not produce header egress output.

## Topology

```
source ──TCP──> proxy ──multicast──> listener1 ──header-egress──> UDP sink (:9107)
                                     listener2  (no header egress)
                                     listener3  (no header egress)
```

## Assertions

| Metric / Check                    | Expected                |
|-----------------------------------|-------------------------|
| `bsl_header_forwarded_total` (L1) | == BLOCK_COUNT          |
| `bsl_header_egress_errors_total`  | == 0                    |
| Sink datagram count               | == BLOCK_COUNT          |
| `bsl_header_forwarded_total` (L2) | == 0 (not configured)   |
| `bsl_header_forwarded_total` (L3) | == 0 (not configured)   |

## Tunables

| Variable             | Default            | Description                          |
|----------------------|--------------------|--------------------------------------|
| `PROXY_TCP_ADDR`     | `[fd20::2]:9002`   | Proxy TCP ingress address            |
| `BLOCK_COUNT`        | `20`               | Number of block announcements to send|
| `SUBTREES_PER_BLOCK` | `4`                | Subtree hashes per announcement      |
| `HEADER_SINK_PORT`   | `9107`             | UDP port for header egress sink      |

## Run

```bash
bash scenarios/35-block-header-egress/run.sh
```
