# Scenario 12 -- Expected Results

## Setup

* Driver: `subtx-gen` on `source` VM with burst gap injection
  (`-seq-gap-every 50 -seq-gap-size 3 -seq-gap-delay 500ms`).
* Endpoint: single retry endpoint (`retry1`), in-memory cache.
* Listeners (`listener1..3`) with `RETRY_ENDPOINTS=[fd20::24]:9300`.

## Pass criteria

| Counter | Expectation |
|--------------------------------------|--------------------------------------|
| `bsl_gaps_detected_total` | > 0 |
| `bsl_nacks_dispatched_total` | > 0 |
| `bre_nack_requests_total` | > 0 |
| `bre_rate_limit_drops_total` | > 0 |
| `bre_retransmits_total` | > 0 |
| `bsl_gaps_suppressed_total` | > 0 |

## Notes

The rate-limit assertion (`bre_rate_limit_drops_total > 0`) is a WARN, not a
hard FAIL, because it depends on the retry endpoint's RL config. If the RL
thresholds are very generous, the burst may not trigger them. In that case,
tighten the RL config in `retry-hosts.yml` or increase PPS/decrease
SEQ_GAP_EVERY.

## Last run

Not yet executed.
