//go:build tpnil

// Package qualify holds the ISI-5 qualifications of the portable joined
// configuration that run against its containers directly.
//
// TestISI5_V010_RepairUnderLoss is ISI-5 V010, the repair criterion: with a
// declared loss applied between the fabric and the delivery edge, every gap the
// edge sees on the fabric segment is detected and then repaired or reported
// unrecovered; within the declared loss nothing is unrecovered, every
// transaction crosses its lane once (no second financial effect), and the
// submission and proof paths (V008, V009) still complete. The impairment is
// applied to UDP only, so the delivery lanes (TCP from the edge to its bridges)
// are never impaired.
package qualify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

type v010Env struct {
	fixtures, arcade, rpc, rpcUser, rpcPass, mineTo string
	listener, retry, listenerMetrics, retryMetrics  string
	bridgeMetrics                                   string
	lossPct                                         float64
}

func need(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Fatalf("%s is required", name)
	}
	return v
}

func loadV010Env(t *testing.T) v010Env {
	t.Helper()
	loss, err := strconv.ParseFloat(need(t, "TPNIL_DECLARED_LOSS_PCT"), 64)
	if err != nil || loss <= 0 || loss >= 50 {
		t.Fatalf("TPNIL_DECLARED_LOSS_PCT %q: want a percentage in (0, 50)", os.Getenv("TPNIL_DECLARED_LOSS_PCT"))
	}
	return v010Env{
		fixtures:        need(t, "TPNIL_FIXTURE_SET"),
		arcade:          strings.TrimRight(need(t, "TPNIL_ARCADE_URL"), "/"),
		rpc:             need(t, "TPNIL_TERANODE_RPC_URL"),
		rpcUser:         need(t, "TPNIL_TERANODE_RPC_USER"),
		rpcPass:         need(t, "TPNIL_TERANODE_RPC_PASSWORD"),
		mineTo:          need(t, "TPNIL_MINE_ADDRESS"),
		listener:        need(t, "TPNIL_LISTENER_CONTAINER"),
		retry:           need(t, "TPNIL_RETRY_CONTAINER"),
		listenerMetrics: need(t, "TPNIL_LISTENER_METRICS"),
		retryMetrics:    need(t, "TPNIL_RETRY_METRICS"),
		bridgeMetrics:   need(t, "TPNIL_TERANODE_BRIDGE_METRICS"),
		lossPct:         loss,
	}
}

type efFixture struct {
	Schema string `json:"schema"`
	TxID   string `json:"txid"`
	EFHex  string `json:"efHex"`
	RawHex string `json:"rawHex"`
}

func TestISI5_V010_RepairUnderLoss(t *testing.T) {
	e := loadV010Env(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	all := readFixtures(t, e.fixtures)
	const edge = 8 // lossless transactions before and after the impaired run
	if len(all) < 2*edge+10 {
		t.Fatalf("%d fixtures; need at least %d", len(all), 2*edge+10)
	}
	warmup, fixtures, trailer := all[:edge], all[edge:len(all)-edge], all[len(all)-edge:]
	t.Logf("fixtures: %d Extended Format transactions, each spending a different mature coinbase output: %d lossless before, %d under the impairment, %d lossless after",
		len(all), len(warmup), len(fixtures), len(trailer))

	// Precondition: the retransmission endpoint is fed upstream of the
	// impairment. It joins the fabric groups itself; a tee from the delivery
	// edge would carry only what survived the loss.
	renv := containerEnv(t, ctx, e.retry)
	if renv["MC_JOIN_ENABLED"] != "true" || renv["TEE_LISTEN"] != "" {
		t.Fatalf("retransmission endpoint is not fed upstream: MC_JOIN_ENABLED=%q TEE_LISTEN=%q", renv["MC_JOIN_ENABLED"], renv["TEE_LISTEN"])
	}
	lenv := containerEnv(t, ctx, e.listener)
	if lenv["LANE_TX"] == "" || lenv["LANE_SUBTREE"] == "" || lenv["LANE_BLOCK"] == "" {
		t.Fatalf("delivery edge has no tx, subtree and block lanes: %v", lenv)
	}
	t.Logf("V010 precondition: the retransmission endpoint joins the fabric groups itself (MC_JOIN_ENABLED=true, no tee), upstream of the declared impairment; the delivery edge delivers on its tx, subtree and block lanes")

	// Gap detection is inferential: a lost frame is known only when a later
	// frame of its flow arrives. A lossless warmup (transactions and a block)
	// gives every flow a baseline at the delivery edge before the impairment,
	// and a lossless trailer extends every flow past its last loss, so each
	// loss under the impairment has a successor that exposes it.
	warmStart := snapshot(t, ctx, e)
	submitAll(t, ctx, e, warmup)
	waitLanes(t, ctx, e, warmStart, len(warmup))
	mine(t, ctx, e, 1)
	time.Sleep(5 * time.Second)

	// Impairment: declared loss on IPv6 UDP into the delivery edge's fabric
	// link. Lane TCP takes an unimpaired band.
	veth := hostVeth(t, ctx, e.listener)
	impair(t, ctx, veth, e.lossPct)
	lifted := false
	t.Cleanup(func() {
		if !lifted {
			_ = unimpair(context.Background(), veth)
		}
	})
	t.Logf("V010 impairment: %.1f%% loss on IPv6 UDP into the delivery edge's fabric link (%s, host side); delivery lanes unimpaired", e.lossPct, veth)

	before := snapshot(t, ctx, e)
	dumpMetrics(t, ctx, e, "before")
	submitAll(t, ctx, e, fixtures)
	waitLanes(t, ctx, e, before, len(fixtures))

	// Mine while the impairment holds, so the block's own objects cross the
	// impaired link too; then lift it and run the lossless trailer.
	blocks := mine(t, ctx, e, 1)
	time.Sleep(3 * time.Second)
	if err := unimpair(ctx, veth); err != nil {
		t.Fatalf("lifting the impairment before the lossless trailer: %v", err)
	}
	lifted = true
	submitAll(t, ctx, e, trailer)
	waitLanes(t, ctx, e, before, len(fixtures)+len(trailer))
	blocks = append(blocks, mine(t, ctx, e, 1)...)
	drainGaps(t, ctx, e, before)
	after := snapshot(t, ctx, e)
	dumpMetrics(t, ctx, e, "after")
	d := after.minus(before)
	t.Logf("V010 accounting: gaps detected %d, repaired %d, unrecovered %d (late-filled %d); NACKs %d; retransmissions %d; tx lane %d objects, %d drops; processor bridge %d",
		d.detected, d.repaired, d.unrecovered, d.lateFilled, d.nacks, d.retransmits, d.laneTx, d.laneTxDrops, d.bridgeTx)
	proved := waitMined(t, ctx, e, append(append([]efFixture{}, fixtures...), trailer...), blocks)
	if d.detected == 0 {
		t.Fatalf("no gap was detected under %.1f%% loss; the impairment did not reach the fabric segment", e.lossPct)
	}
	if d.repaired+d.unrecovered != d.detected {
		t.Fatalf("gaps detected %d != repaired %d + unrecovered %d: a gap was neither repaired nor reported", d.detected, d.repaired, d.unrecovered)
	}
	if d.unrecovered != 0 {
		t.Fatalf("%d gaps unrecovered within the declared %.1f%% loss", d.unrecovered, e.lossPct)
	}
	if d.retransmits == 0 {
		t.Fatal("gaps were repaired without a retransmission from the endpoint")
	}
	t.Logf("V010 every gap on the fabric segment was detected and repaired: %d detected = %d repaired + 0 unrecovered", d.detected, d.repaired)

	n := int64(len(fixtures) + len(trailer))
	if d.laneTx != n || d.bridgeTx != n || d.laneTxDrops != 0 {
		t.Fatalf("tx lane carried %d objects and the processor bridge received %d for %d transactions (%d lane drops); sent must equal delivered", d.laneTx, d.bridgeTx, n, d.laneTxDrops)
	}
	t.Logf("V010 sent %d = delivered %d + unrecovered 0: each transaction crossed the tx lane once and reached the processor bridge once, so no transaction had a second financial effect", n, d.laneTx)
	t.Logf("V010 V008 and V009 hold under the impairment: all %d transactions were mined (blocks %s, %s) and Broadcaster returned %d proofs", n, blocks[0], blocks[1], proved)
}

// readFixtures reads the tpnil-arcade-teranode-fixtures@1.0.0 document at
// path, strictly: a set of Extended Format fixtures, each spending a different
// output.
func readFixtures(t *testing.T, path string) []efFixture {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var set struct {
		Schema   string      `json:"schema"`
		Fixtures []efFixture `json:"fixtures"`
	}
	if err := dec.Decode(&set); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if set.Schema != "tpnil-arcade-teranode-fixtures@1.0.0" || len(set.Fixtures) == 0 {
		t.Fatalf("%s: schema %q with %d fixtures", path, set.Schema, len(set.Fixtures))
	}
	seen := map[string]bool{}
	for i, fx := range set.Fixtures {
		if fx.Schema != "tpnil-arcade-teranode-fixture@1.0.0" || len(fx.TxID) != 64 || seen[fx.TxID] {
			t.Fatalf("%s: fixture %d: schema %q txid %q (repeated: %v)", path, i+1, fx.Schema, fx.TxID, seen[fx.TxID])
		}
		seen[fx.TxID] = true
	}
	return set.Fixtures
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func containerEnv(t *testing.T, ctx context.Context, name string) map[string]string {
	t.Helper()
	out, err := run(ctx, "sudo", "-n", "docker", "inspect", name, "--format", "{{json .Config.Env}}")
	if err != nil {
		t.Fatalf("docker inspect %s: %v: %s", name, err, out)
	}
	var list []string
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range list {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// hostVeth returns the host-side peer of the container's fabric0. Traffic the
// host sends out of it is traffic the container receives.
func hostVeth(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	pid, err := run(ctx, "sudo", "-n", "docker", "inspect", name, "--format", "{{.State.Pid}}")
	if err != nil || pid == "0" {
		t.Fatalf("container %s is not running: %v %s", name, err, pid)
	}
	link, err := run(ctx, "sudo", "-n", "nsenter", "--net=/proc/"+pid+"/ns/net", "ip", "-o", "link", "show", "fabric0")
	if err != nil {
		t.Fatalf("fabric0 in %s: %v: %s", name, err, link)
	}
	i := strings.Index(link, "@if")
	if i < 0 {
		t.Fatalf("fabric0 in %s has no peer: %s", name, link)
	}
	idx := strings.TrimRight(strings.Fields(link[i+3:])[0], ":")
	out, err := run(ctx, "ip", "-o", "link")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 1 && strings.TrimSuffix(f[0], ":") == idx {
			return strings.SplitN(strings.TrimSuffix(f[1], ":"), "@", 2)[0]
		}
	}
	t.Fatalf("no host interface with index %s", idx)
	return ""
}

func impair(t *testing.T, ctx context.Context, veth string, loss float64) {
	t.Helper()
	_ = unimpair(ctx, veth) // none may be installed yet
	for _, args := range [][]string{
		// Every packet goes to band 2 (1:2) unless the filter moves it.
		{"qdisc", "add", "dev", veth, "root", "handle", "1:", "prio", "bands", "3", "priomap",
			"1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1", "1"},
		{"qdisc", "add", "dev", veth, "parent", "1:3", "handle", "30:", "netem", "loss", fmt.Sprintf("%.1f%%", loss)},
		// IPv6 UDP (next header 17) takes the lossy band.
		{"filter", "add", "dev", veth, "parent", "1:", "protocol", "ipv6", "prio", "1", "u32",
			"match", "ip6", "protocol", "17", "0xff", "flowid", "1:3"},
	} {
		if out, err := run(ctx, "sudo", append([]string{"-n", "tc"}, args...)...); err != nil {
			t.Fatalf("tc %v: %v: %s", args, err, out)
		}
	}
}

func unimpair(ctx context.Context, veth string) error {
	if out, err := run(ctx, "sudo", "-n", "tc", "qdisc", "del", "dev", veth, "root"); err != nil {
		return fmt.Errorf("tc qdisc del dev %s root: %w: %s", veth, err, out)
	}
	return nil
}

type counts struct {
	detected, repaired, unrecovered, lateFilled, nacks, retransmits int64
	laneTx, laneTxDrops, bridgeTx                                   int64
}

func (a counts) minus(b counts) counts {
	return counts{
		detected: a.detected - b.detected, repaired: a.repaired - b.repaired,
		unrecovered: a.unrecovered - b.unrecovered, lateFilled: a.lateFilled - b.lateFilled,
		nacks: a.nacks - b.nacks, retransmits: a.retransmits - b.retransmits,
		laneTx: a.laneTx - b.laneTx, laneTxDrops: a.laneTxDrops - b.laneTxDrops, bridgeTx: a.bridgeTx - b.bridgeTx,
	}
}

func snapshot(t *testing.T, ctx context.Context, e v010Env) counts {
	t.Helper()
	l := scrape(t, ctx, e.listenerMetrics)
	r := scrape(t, ctx, e.retryMetrics)
	b := scrape(t, ctx, e.bridgeMetrics)
	return counts{
		detected:    sum(l, "bsl_gaps_detected_total", ""),
		repaired:    sum(l, "bsl_gaps_suppressed_total", ""),
		unrecovered: sum(l, "bsl_gaps_unrecovered_total", ""),
		lateFilled:  sum(l, "bsl_gaps_late_filled_total", ""),
		nacks:       sum(l, "bsl_nacks_dispatched_total", ""),
		retransmits: sum(r, "bre_retransmits_total", "") + sum(r, "bre_unicast_retransmits_total", ""),
		laneTx:      sum(l, "bsl_lane_objects_total", `lane="tx"`),
		laneTxDrops: sum(l, "bsl_lane_drops_total", `lane="tx"`),
		bridgeTx:    sum(b, "teranode_bridge_lane_objects_total", `lane="tx"`),
	}
}

type series struct {
	name, labels string
	value        float64
}

func scrape(t *testing.T, ctx context.Context, url string) []series {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape %s: HTTP %d", url, resp.StatusCode)
	}
	var out []series
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		sp := strings.LastIndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[sp+1:], 64)
		if err != nil {
			continue
		}
		name, labels := line[:sp], ""
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name, labels = name[:i], name[i:]
		}
		out = append(out, series{name, labels, v})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scrape %s: %v", url, err)
	}
	return out
}

func sum(s []series, name, label string) int64 {
	var total float64
	for _, x := range s {
		if x.name == name && strings.Contains(x.labels, label) {
			total += x.value
		}
	}
	return int64(total)
}

func post(t *testing.T, ctx context.Context, url string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func rpcCall(t *testing.T, ctx context.Context, e v010Env, method string, params []any, result any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": "v010", "method": method, "params": params})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.rpc, bytes.NewReader(body))
	req.SetBasicAuth(e.rpcUser, e.rpcPass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if env.Error != nil {
		t.Fatalf("%s: %s", method, env.Error.Message)
	}
	if result != nil {
		if err := json.Unmarshal(env.Result, result); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
}

func mine(t *testing.T, ctx context.Context, e v010Env, n int) []string {
	t.Helper()
	var hashes []string
	rpcCall(t, ctx, e, "generatetoaddress", []any{n, e.mineTo}, &hashes)
	if len(hashes) != n {
		t.Fatalf("generatetoaddress returned %d hashes", len(hashes))
	}
	return hashes
}

// waitLanes waits until the tx lane has carried one object per fixture (or
// the deadline passes; the accounting then reports the shortfall).
func waitLanes(t *testing.T, ctx context.Context, e v010Env, before counts, n int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot(t, ctx, e).minus(before).bridgeTx >= int64(n) {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%d of %d transactions reached the processor bridge within 90 s", snapshot(t, ctx, e).minus(before).bridgeTx, n)
}

// drainGaps waits until every detected gap is repaired or reported, so the
// accounting is read with no gap still pending.
func drainGaps(t *testing.T, ctx context.Context, e v010Env, before counts) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		d := snapshot(t, ctx, e).minus(before)
		if d.detected > 0 && d.repaired+d.unrecovered >= d.detected {
			time.Sleep(3 * time.Second) // let a late retransmission land before the final read
			return
		}
		time.Sleep(time.Second)
	}
}

// submitAll submits each fixture through Broadcaster.
func submitAll(t *testing.T, ctx context.Context, e v010Env, fixtures []efFixture) {
	t.Helper()
	for i, fx := range fixtures {
		ef, err := hex.DecodeString(fx.EFHex)
		if err != nil {
			t.Fatalf("fixture %d: %v", i+1, err)
		}
		if code := post(t, ctx, e.arcade+"/tx", ef); code/100 != 2 {
			t.Fatalf("fixture %d (%s): Broadcaster answered %d", i+1, fx.TxID, code)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// dumpMetrics writes the delivery edge's and retransmission endpoint's metrics
// to TPNIL_METRICS_DIR, when it is set, for reading the accounting by flow.
func dumpMetrics(t *testing.T, ctx context.Context, e v010Env, label string) {
	t.Helper()
	dir := os.Getenv("TPNIL_METRICS_DIR")
	if dir == "" {
		return
	}
	for name, url := range map[string]string{"listener": e.listenerMetrics, "retry": e.retryMetrics, "teranode-bridge": e.bridgeMetrics} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("metrics %s: %v", name, err)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err := os.WriteFile(dir+"/"+label+"-"+name+".prom", b, 0o644); err != nil {
			t.Errorf("metrics %s: %v", name, err)
		}
	}
}

// waitMined waits for Broadcaster to report every fixture MINED in one of
// blocks with a merkle path, and returns how many it proved.
func waitMined(t *testing.T, ctx context.Context, e v010Env, fixtures []efFixture, blocks []string) int {
	t.Helper()
	pending := map[string]bool{}
	for _, fx := range fixtures {
		pending[fx.TxID] = true
	}
	deadline := time.Now().Add(5 * time.Minute)
	for len(pending) > 0 && time.Now().Before(deadline) {
		for txid := range pending {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.arcade+"/tx/"+txid, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				continue
			}
			var st struct {
				TxStatus   string `json:"txStatus"`
				BlockHash  string `json:"blockHash"`
				MerklePath string `json:"merklePath"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&st)
			_ = resp.Body.Close()
			if st.TxStatus == "MINED" && st.MerklePath != "" {
				if st.BlockHash != "" && !contains(blocks, st.BlockHash) {
					t.Fatalf("%s mined in %s, want one of %v", txid, st.BlockHash, blocks)
				}
				delete(pending, txid)
			}
		}
		if len(pending) > 0 {
			time.Sleep(2 * time.Second)
		}
	}
	if len(pending) > 0 {
		for txid := range pending {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.arcade+"/tx/"+txid, nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
				_ = resp.Body.Close()
				t.Logf("not proved: %s: Broadcaster says %s", txid, b)
			}
		}
		t.Fatalf("%d of %d transactions not reported MINED with a proof", len(pending), len(fixtures))
	}
	return len(fixtures)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
