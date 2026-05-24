# Expected Results

This is a data-generation scenario, not a strict test. Expected behavior:

## Frame delivery rates

Based on listener filter configuration from `ansible/listener-hosts.yml`:

- **listener1**: No filters → receives all frames, forwards all
- **listener2**: Half shards + subtree-exclude → receives ~50%, forwards ~87.5% of received
- **listener3**: Single subtree-include → receives ~12.5%, forwards all received

At 1000 pps for 24 hours (86,400,000 frames total):

| Listener | Expected received | Expected forwarded |
|-----------|-------------------|--------------------|
| listener1 | ~86,400,000 | ~86,400,000 |
| listener2 | ~43,200,000 | ~37,800,000 |
| listener3 | ~10,800,000 | ~10,800,000 |

## Health indicators

- **bsl_gaps_detected_total**: Should be very low (<0.1% of received frames)
- **bsl_nacks_dispatched_total**: Should be minimal (indicates reliable delivery)
- **bsl_frames_dropped_total**: Should match filter configuration (not delivery failures)
- **bsl_egress_errors_total**: Should be zero

## Dashboard visualization

This scenario generates sufficient data points for:
- 5-minute time windows in Grafana
- Rate calculations (frames/sec)
- Trend analysis over sustained load
