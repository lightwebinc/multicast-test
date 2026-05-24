# Expected outputs — scenario 07

```
==> Snapshot metrics (before)
-- generator: pps=1000 duration=10s payload=mixed -> ~10000 frames --
subtx-gen dev: addr=[fd20::2]:9000 frame=v2 payload=mixed pps=1000 workers=2 subtrees=8 duration=10s
...
done: sent=10000 errors=0 elapsed=10.00s avg_pps=1000
==> Allow egress pipeline to drain
==> Snapshot metrics (after)
PASS  listener1 forwarded (mixed): got ~10000
PASS  listener2 forwarded (mixed, shardxsubtree filter): got ~4375
PASS  listener3 forwarded (mixed, subtree-include): got ~1250
PASS  listener1 bad_frame=0 (mixed BRC-124/BRC-128 traffic transparent)
PASS  listener2 bad_frame=0 (mixed BRC-124/BRC-128 traffic transparent)
PASS  listener3 bad_frame=0 (mixed BRC-124/BRC-128 traffic transparent)
Scenario 07: PASS
```
