# Bitcoin Multicast Test - Troubleshooting Guide

This guide documents common issues and solutions discovered during testing of the shard-proxy multicast test lab.

## Table of Contents

- [Listener Issues](#listener-issues)
- [Beacon Discovery Issues](#beacon-discovery-issues)
- [Dashboard Issues](#dashboard-issues)
- [Multicast Delivery Problems](#multicast-delivery-problems)
- [Connectivity Issues](#connectivity-issues)
- [Performance Testing](#performance-testing)
- [Service Management](#service-management)

## Listener Issues

### Metrics scrape returns no data from listener1..3

**Symptoms:** Prometheus target shows `DOWN`, or `curl http://10.10.10.31:9200/metrics` from the LXD host times out.

**Root cause:** `mgmt_cidrs_v4` in `ansible/listener-hosts.yml` is
missing the scraper's source CIDR. The firewall role drops inbound TCP
:9200 unless the source IP is allow-listed.

**Fix:** ensure `mgmt_cidrs_v4` includes `10.10.10.0/24` (LXD mgmt
bridge) and re-run `ansible/run-deploy.sh`.

### Listener forwards 0 frames even though proxy emits multicast

**Symptoms:** `bsp_packets_forwarded_total` on proxy increments, but
`bsl_frames_received_total` on every listener stays at 0.

**Likely causes:**

1. `ingress_iface` in the inventory is `eth0` (wrong) instead of `enp6s0`.
2. Bridge MLD querier disabled — `cat /sys/devices/virtual/net/lxdbr1/bridge/multicast_querier` returns 0.
3. `shard_bits` on listener differs from proxy. Must match exactly.

### Scenario asserts fail with counts at ~50% expected

**Symptoms:** `listener2 forwarded` delta is about half the expected
value; `shard_filter` dropped count is zero.

**Root cause:** `shard_include` didn't take effect — either the
`SHARD_INCLUDE` env var wasn't parsed or the listener joined only one
of the two shard groups. Verify with:

```bash
lxc exec listener2 -- ip maddr show dev enp6s0 | grep ff05
```

Expect two `ff05::b:0` / `ff05::b:1` entries.

### Pinned subtree IDs don't match what the generator emits

**Symptoms:** listener3 (subtree-include) forwards ~0 frames instead
of ~⅛ of the stream.

**Root cause:** the `-subtree-seed` passed to `subtx-gen` differs from
the seed used to generate the pinned IDs in the inventory. Regenerate
and compare:

```bash
lxc exec source -- subtx-gen -subtrees 8 -subtree-seed 'multicast-lab-bsv' -print-subtrees
grep subtree_ ansible/listener-hosts.yml
```

## Beacon Discovery Issues

### Retry endpoints not appearing in listener registry

**Symptoms:** `journalctl -u shard-listener | grep "upserted endpoint"` shows
no output even after 10+ seconds. `bsl_gaps_unrecovered_total` climbs because
the registry is empty and no NACKs can be dispatched.

**Likely cause — wrong multicast egress interface:**
On multi-homed hosts (management NIC `enp5s0` + fabric NIC `enp6s0`), the Linux
kernel may route `ff05::` multicast via the default-route interface (management)
instead of the fabric NIC, causing beacons to arrive on the wrong interface and
be dropped by the listener's nftables rule.

**Diagnosis:**

```bash
# On the LXD host, capture on the fabric bridge
sudo tcpdump -i lxdbr1 -n 'udp and dst port 9300' &
# Wait 6s; should see packets from fd20::24, fd20::25, fd20::26
```

**Fix:** `retry-endpoint/beacon/beacon.go` sets `IPV6_MULTICAST_IF` via
`syscall.SetsockoptInt` after `net.DialUDP`. If beacons still don't arrive,
verify the `MC_IFACE` env var on the retry endpoint is set to the fabric
interface (`enp6s0`).

**Verification:**

```bash
# Confirm beacons arriving at listener
lxc exec listener1 -- journalctl -u shard-listener --since "1 min ago" | grep upsert
# Expected: discovery: upserted endpoint addr=... tier=0 pref=128
```

---

### High `bsl_gaps_unrecovered_total` with multiple retry endpoints

**Symptoms:** Scenario 13 shows `gaps_unrecovered > 0`. Each MISS response from
a retry endpoint consumes one retry counter. With 3 beacon + 3 static seed
entries in the registry (6 total) and `NACK_MAX_RETRIES=5` (default), the
counter is exhausted at entry #5 — one short of the last seed.

**Fix:** Set `NACK_MAX_RETRIES=8` on all listeners:

```bash
for vm in listener1 listener2 listener3; do
  lxc exec $vm -- sed -i 's/^NACK_MAX_RETRIES=.*/NACK_MAX_RETRIES=8/' \
    /etc/shard-listener/config.env
  lxc exec $vm -- systemctl restart shard-listener
done
```

Persist the change in `ansible/listener-hosts.yml` under each listener host's
vars block.

---

### `bsl_gaps_detected_total` is ~10× higher than expected

**Symptoms:** After scenario 13 starts, gap detection count far exceeds the
number of natural multicast losses. `bsl_gaps_suppressed_total` is low
relative to `bsl_gaps_detected_total`, so most gaps appear unrecovered.

**Root cause:** Phantom gaps from retransmitted frames. Before the 2026-05-05
fix to `nack/nack.go`, `Observe()` created a new gap entry for every
out-of-order or retransmitted frame (`seqNum < lastSeqNum`). Each retransmit
from a retry endpoint triggered dozens of false gaps.

**Fix:** Already applied to `shard-listener/nack/nack.go`. Rebuild and
redeploy the listener binary. After the fix, `bsl_gaps_detected_total` should
match the actual number of multicast delivery losses.

---

## Dashboard Issues

### Grafana Dashboard Shows No Data

**Symptoms:**

- Dashboard URL loads but shows "No data"
- Prometheus metrics endpoint accessible but dashboard empty

**Root Cause:**

- Prometheus not configured to scrape shard-proxy metrics
- Dashboard queries using `rate()` functions without sufficient data

**Solution:**

```bash
# 1. Add shard-proxy to Prometheus config
lxc exec metrics -- cp /etc/prometheus/prometheus.yml /etc/prometheus/prometheus.yml.backup

lxc exec metrics -- tee /etc/prometheus/prometheus.yml > /dev/null << 'EOF'
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'prometheus'
    scrape_interval: 5s
    static_configs:
      - targets: ['localhost:9090']

  - job_name: 'node'
    static_configs:
      - targets: ['localhost:9100']

  - job_name: 'shard-proxy'
    scrape_interval: 5s
    static_configs:
      - targets: ['10.10.10.20:9100']
EOF

# 2. Restart Prometheus
lxc exec metrics -- systemctl restart prometheus

# 3. Verify targets are up
lxc exec metrics -- curl -s http://localhost:9090/api/v1/targets | jq '.data.activeTargets[] | {job: .labels.job, health: .health}'

# 4. Update dashboard to use absolute values instead of rate()
# Access dashboard: http://10.10.10.142:3000/d/shard-proxy-metrics
# Login: admin/admin
```

**Verification:**

```bash
# Check metrics are accessible
lxc exec metrics -- curl -s 'http://localhost:9090/api/v1/query?query=bsp_packets_forwarded_total'

# Should show data like:
# {"status":"success","data":{"resultType":"vector","result":[...]}}
```

## Multicast Delivery Problems

### Only recv1 Receives Traffic

**Symptoms:**

- Only recv1 gets multicast traffic; recv2/recv3 get nothing
- Proxy `bsp_flow_packets_total` metrics show forwarding across all groups
- Bridge MDB entries exist for recv2/recv3 taps
- Host-level tcpdump on recv2/recv3 tap interfaces shows packets arriving

**Correct Diagnostic Flow:**

```bash
# Step 1: Run the full verification script
bash 08-verify.sh
# Expected: MDB has tap entries for ff05::b:2 (recv2) and ff05::b:1+ff05::b:3 (recv3)
# Expected: multicast_snooping=1, multicast_querier=1
# Expected: lxd-bridge-mcast-querier.service active

# Step 2: Confirm correct MDB entries exist (MUST have all 3 receivers)
bridge mdb show dev lxdbr1 | grep ff05
# recv1 tap: ff05::, ff05::b:1, ff05::b:2, ff05::b:3
# recv2 tap: ff05::b:2
# recv3 tap: ff05::b:1, ff05::b:3

# Step 3: Test live delivery using RX counters (more reliable than tcpdump timing)
R2=$(lxc exec recv2 -- ip -s link show enp6s0 | awk '/RX:/{getline; print $2}')
R3=$(lxc exec recv3 -- ip -s link show enp6s0 | awk '/RX:/{getline; print $2}')
lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 4 -spread
sleep 2
R2_NEW=$(lxc exec recv2 -- ip -s link show enp6s0 | awk '/RX:/{getline; print $2}')
R3_NEW=$(lxc exec recv3 -- ip -s link show enp6s0 | awk '/RX:/{getline; print $2}')
echo "recv2 delta: $((R2_NEW - R2))  recv3 delta: $((R3_NEW - R3))"
# Expected: recv2 delta=1, recv3 delta=2 (spread sends one per group)
```

**Root Cause: Stale MDB after incomplete reboot/restart**

The most common cause is the bridge MDB not being fully populated at boot. This happens when `mcast-join.service` on the receiver VMs starts before the bridge querier has fully initialized.

**Fix:**

```bash
# Restart bridge querier first, then receivers sequentially
sudo systemctl restart lxd-bridge-mcast-querier.service
sleep 3
for vm in recv1 recv2 recv3; do
  lxc exec $vm -- systemctl restart mcast-join.service
  sleep 5
done
# Then re-run 08-verify.sh to confirm MDB is correct
bash 08-verify.sh
```

**Note on tcpdump inside VMs:**
tcpdump can appear to capture nothing inside the VM if timing is off (the 8s window may miss traffic). Always use `ip -s link show enp6s0` RX counter deltas for reliable delivery confirmation.

**Note on br_netfilter (Multipass):**
`br_netfilter` is loaded by `/etc/modules-load.d/snap.multipass.conf`, setting `net.bridge.bridge-nf-call-ip6tables=1`. This is **not** the cause of multicast issues — there are no blocking ip6tables/nftables rules on lxdbr1 traffic. It is safe to leave as-is.

**Note on additional physical interfaces (enp2s0f0/enp2s0f1, mac644e4f9e):**
These interfaces from the 10gb-direct-testing setup are **not** bridge members on lxdbr0/lxdbr1 and have no IPv6 addresses in the fd20::/64 space. They do not interfere with multicast.

## Connectivity Issues

### Source Cannot Reach Proxy

**Symptoms:**

- send-test-frames fails with connection errors
- Proxy metrics don't increase
- Traffic doesn't flow

**Diagnostics:**

```bash
# 1. Check proxy listening
lxc exec proxy -- ss -ulnp | grep 9000

# 2. Test connectivity
lxc exec source -- nc -u -z -w 2 fd20::2 9000

# 3. Check routing
lxc exec source -- ip -6 route get fd20::2

# 4. Verify addresses
lxc exec proxy -- ip addr show enp6s0 | grep fd20
lxc exec source -- ip addr show enp6s0 | grep fd20
```

**Solutions:**

```bash
# Use correct proxy address format
lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 4

# Test with simple UDP if send-test-frames fails
echo "test" | lxc exec source -- socat - UDP6-DATAGRAM:[fd20::2]:9000 2>/dev/null
```

## Performance Testing

### send-test-frames Flag Issues

**Common Mistake:** Using flags that don't exist

```bash
# ❌ WRONG - these flags don't exist
lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -pps 5000 -duration 24h

# ✅ CORRECT - use available flags
lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 0 -interval 1
```

**Available Flags:**

```bash
-addr string        # Proxy listen address (default: "[::1]:9000")
-count int          # Number of frames (0 = infinite)
-interval int       # Milliseconds between frames (default: 200)
-shard-bits uint    # Shard bits for group prediction (default: 2)
-spread             # Send one per group, ignores -count
```

**24-Hour Test Commands:**

```bash
# High rate (maximum)
timeout 24h lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 0 -interval 0

# Controlled rate (~1000 PPS)
timeout 24h lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 0 -interval 1

# Burst mode (recommended)
while true; do
  echo "Starting burst: $(date)"
  lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 100000 -interval 0
  echo "Burst complete, sleeping..."
  sleep 300  # 5 minute break
done
```

### Expected Performance Metrics

**Packet Rate Estimates:**

- `-interval 0`: Maximum rate (CPU/network limited)
- `-interval 1`: ~1,000 PPS
- `-interval 10`: ~100 PPS
- `-interval 100`: ~10 PPS

**24-Hour Estimates:**

- 1000 PPS: ~86M packets
- 100 PPS: ~8.6M packets

## Service Management

### Restarting All Services

```bash
# 1. Restart mcast-join on all receivers
for vm in recv1 recv2 recv3; do
  lxc exec $vm -- systemctl restart mcast-join.service
done

# 2. Restart bridge querier on host
sudo systemctl restart lxd-bridge-mcast-querier.service

# 3. Restart proxy if needed
lxc exec proxy -- systemctl restart shard-proxy.service

# 4. Verify all services
for vm in recv1 recv2 recv3; do
  echo "=== $vm ==="
  lxc exec $vm -- systemctl status mcast-join.service --no-pager | head -3
done
```

### Checking Service Status

```bash
# Proxy health
lxc exec proxy -- curl -s http://localhost:9100/healthz

# Proxy metrics
lxc exec proxy -- curl -s http://localhost:9100/metrics | grep bsp_packets_forwarded_total

# Mcast-join services
for vm in recv1 recv2 recv3; do
  echo "=== $vm ==="
  lxc exec $vm -- systemctl is-active mcast-join.service
done
```

## Quick Reference Commands

### Essential Verification Commands

```bash
# 1. Check dashboard access
curl -s -u admin:admin http://10.10.10.142:3000/api/datasources | jq '.[] | select(.name == "prometheus")'

# 2. Verify proxy metrics
lxc exec proxy -- curl -s http://localhost:9100/healthz

# 3. Check multicast groups
for vm in recv1 recv2 recv3; do
  lxc exec $vm -- ip -6 maddr show dev enp6s0 | grep ff05
done

# 4. Test traffic flow
lxc exec source -- send-test-frames -addr '[fd20::2]:9000' -shard-bits 2 -count 4 -spread
```

### Cleanup Commands

```bash
# Stop all traffic generation
lxc exec source -- pkill -f send-test-frames

# Stop receivers
for vm in recv1 recv2 recv3; do
  lxc exec $vm -- pkill -f recv-test-frames
done

# Collect final metrics
echo "Final packet counts:"
lxc exec proxy -- curl -s http://localhost:9100/metrics | grep bsp_packets_forwarded_total
```

## Common Workarounds

### When Multicast Delivery Fails

If VMs don't receive multicast traffic (but proxy works):

1. **Accept the limitation** - Focus testing on proxy functionality
2. **Use proxy metrics** - All important data is available via Prometheus
3. **Monitor dashboard** - Real-time metrics work regardless of VM delivery
4. **Test proxy performance** - Use perf-test or send-test-frames for load testing

The proxy's core functionality (receiving, sharding, forwarding metrics) works independently of VM multicast delivery.

### Dashboard Query Issues

If dashboard shows no data despite metrics being available:

1. **Use absolute values** instead of `rate()` functions for initial testing
2. **Check time ranges** - Ensure dashboard covers the right time period
3. **Verify data source** - Confirm Prometheus datasource is accessible
4. **Simplify queries** - Start with basic `bsp_packets_forwarded_total` queries

## Recovery Procedures

### Full System Reset

```bash
# 1. Stop all services
for vm in recv1 recv2 recv3; do
  lxc exec $vm -- systemctl stop mcast-join.service
done
lxc exec proxy -- systemctl stop shard-proxy.service

# 2. Restart bridge querier
sudo systemctl restart lxd-bridge-mcast-querier.service

# 3. Start services in order
lxc exec proxy -- systemctl start shard-proxy.service
sleep 5

for vm in recv1 recv2 recv3; do
  lxc exec $vm -- systemctl start mcast-join.service
  sleep 3
done

# 4. Verify functionality
bash 08-verify.sh
bash test-send.sh
```

This troubleshooting guide captures the most common issues encountered during shard-proxy testing and their proven solutions.
