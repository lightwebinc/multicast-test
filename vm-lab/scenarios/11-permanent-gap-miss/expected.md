# Scenario 11 -- Expected Results

## Setup

* Driver: `subtx-gen` on `source` VM, 500 PPS, 10s (no gap injection flags).
* Retry endpoint (`retry1`) restarted to flush cache, then multicast ingress
  blocked via `ip6tables -I INPUT -p udp --dport 9001 -j DROP`.
* NACK server (port 9300) remains reachable — only frame ingress is blocked.
* Listeners (`listener1..3`) with `RETRY_ENDPOINTS=[fd20::24]:9300`.

## Pass criteria

| Counter | Expectation |
|-------------------------------|--------------------------------------|
| `bre_frames_cached_total` | == 0 (ingress blocked) |
| `bsl_gaps_detected_total` | > 0 (natural multicast loss) |
| `bsl_nacks_dispatched_total` | > 0 |
| `bre_nack_requests_total` | > 0 |
| `bre_cache_misses_total` | > 0 |
| `bre_retransmits_total` | == 0 |
| `bsl_gaps_unrecovered_total` | > 0 |

## Last run

Not yet executed.
