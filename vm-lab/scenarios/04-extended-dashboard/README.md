# Scenario 04 — Extended dashboard population test

Drive frames at 1000 pps for an extended duration (24 hours by default) to
populate Grafana dashboards with meaningful time-series data. This scenario
is designed for manual execution in tmux on the source host to visualize system behavior
under sustained load over long periods.

## Expected

All three listeners should receive and forward frames continuously. No strict
assertions — this is a data-generation scenario for dashboard visualization.

Typical rates (based on listener filter configuration):

| Listener | Expected receive rate | Expected forward rate |
|-----------|-----------------------|-----------------------|
| listener1 | 1000 pps | 1000 pps |
| listener2 | ~500 pps | ~437 pps |
| listener3 | ~125 pps | ~125 pps |

## Run (manual in tmux on source host)

```bash
# SSH into source host
ssh $SOURCE_HOST

# Start tmux session (persistent for 24h+)
tmux new -s dashboard-test

# In the tmux session:
cd scenarios/04-extended-dashboard

# Run with default 24-hour duration
bash run.sh

# Or customize duration (e.g., 48 hours, 72 hours)
DURATION=48h bash run.sh
DURATION=72h bash run.sh

# Detach from tmux: Ctrl+B, then D
# Reattach to check progress: tmux attach -t dashboard-test
# List sessions: tmux ls
```

**Important**: Keep the tmux session running for the full duration. The test
generates continuous traffic for dashboard visualization.

## Dashboard monitoring

While the test runs, monitor these dashboards in Grafana:

- **Bitcoin Shard Listener**: Frame rates, drops, gaps, NACKs
- **Bitcoin Shard Proxy**: Ingress/egress rates, worker metrics

Look for:
- Stable frame rates without drops
- Low gap detection rate
- Minimal NACK activity (indicates reliable delivery)
