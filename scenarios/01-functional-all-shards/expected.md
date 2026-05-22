# Expected outputs — scenario 01

```
==> Snapshot metrics (before)
-- generator: pps=1000 duration=10s -> ~10000 frames --
subtx-gen dev: addr=[fd20::2]:9000 frame=v2 pps=1000 workers=2 subtrees=8 duration=10s
...
done: sent=10000 errors=0 elapsed=10.00s avg_pps=1000
==> Allow egress pipeline to drain
==> Snapshot metrics (after)
PASS  listener1 forwarded: got 10000 expected~10000 (tol=.05, diff=0 <= 500)
PASS  listener2 forwarded (shardxsubtree filter): got 4380 expected~4375 (tol=.10, diff=5 <= 437)
PASS  listener3 forwarded (subtree-include): got 1247 expected~1250 (tol=.15, diff=3 <= 187)
Scenario 01: PASS
```
