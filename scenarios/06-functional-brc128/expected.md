# Expected outputs — scenario 06

```
==> Snapshot metrics (before)
-- generator: pps=1000 duration=10s payload=brc128 -> ~10000 frames --
subtx-gen dev: addr=[fd20::2]:9000 frame=v2 payload=brc128 pps=1000 workers=2 subtrees=8 duration=10s
...
done: sent=10000 errors=0 elapsed=10.00s avg_pps=1000
==> Allow egress pipeline to drain
==> Snapshot metrics (after)
PASS  listener1 forwarded: got 10000 expected~10000 (tol=.05, ...)
PASS  listener2 forwarded (shard×subtree filter): got ~4375 (tol=.10, ...)
PASS  listener3 forwarded (subtree-include): got ~1250 (tol=.15, ...)
PASS  listener1 bad_frame=0 (EF payload transparent to header parser)
PASS  listener2 bad_frame=0 (EF payload transparent to header parser)
PASS  listener3 bad_frame=0 (EF payload transparent to header parser)
Scenario 06: PASS
```
