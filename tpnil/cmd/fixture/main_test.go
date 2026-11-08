package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

// fakeNode serves the JSON-RPC methods and asset API routes the tool uses,
// in the shapes Teranode's services/rpc and services/asset/httpimpl return,
// over an in-memory regtest chain.
type fakeNode struct {
	mu       sync.Mutex
	user     string
	password string
	blocks   []*fakeBlock
	txs      map[string]*transaction.Transaction // every transaction, by txid
	mined    map[string]*fakeBlock               // block holding each mined txid
	spent    map[string]bool                     // "txid:vout"
	mempool  []*transaction.Transaction
	throttle int  // asset requests still to answer with 429
	badRoot  bool // header JSON reports a merkle root other than the block's
	reject   bool // sendrawtransaction rejects every transaction
	asset    *httptest.Server
	rpc      *httptest.Server
}

type fakeBlock struct {
	height uint32
	hash   string
	header []byte
	txs    []*transaction.Transaction // the coinbase first
}

func newFakeNode(t *testing.T, user, password string) *fakeNode {
	t.Helper()
	f := &fakeNode{user: user, password: password, txs: map[string]*transaction.Transaction{}, mined: map[string]*fakeBlock{}, spent: map[string]bool{}}
	genesis := script.Script{script.OpTRUE}
	f.mine(1, &genesis)
	f.rpc = httptest.NewServer(http.HandlerFunc(f.serveRPC))
	f.asset = httptest.NewServer(http.HandlerFunc(f.serveAsset))
	t.Cleanup(f.rpc.Close)
	t.Cleanup(f.asset.Close)
	return f
}

// mine appends n blocks whose coinbases pay lock; the first takes the mempool.
func (f *fakeNode) mine(n int, lock *script.Script) []string {
	var hashes []string
	for range n {
		height := uint32(len(f.blocks))
		txs := append([]*transaction.Transaction{coinbaseTx(height, output(50_0000_0000, lock))}, f.mempool...)
		f.mempool = nil
		var ids []*chainhash.Hash
		for _, tx := range txs {
			ids = append(ids, tx.TxID())
		}
		prev := &chainhash.Hash{}
		if height > 0 {
			prev, _ = chainhash.NewHashFromHex(f.blocks[height-1].hash)
		}
		hdr := header80(prev, merkleRoot(ids), height)
		b := &fakeBlock{height: height, hash: chainhash.DoubleHashH(hdr).String(), header: hdr, txs: txs}
		f.blocks = append(f.blocks, b)
		for _, tx := range txs {
			f.txs[tx.TxID().String()] = tx
			f.mined[tx.TxID().String()] = b
		}
		hashes = append(hashes, b.hash)
	}
	return hashes
}

func (f *fakeNode) mempoolSize() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.mempool)
}

func (f *fakeNode) height() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.blocks) - 1
}

func (f *fakeNode) block(hash string) *fakeBlock {
	for _, b := range f.blocks {
		if b.hash == hash {
			return b
		}
	}
	return nil
}

func (f *fakeNode) serveRPC(w http.ResponseWriter, r *http.Request) {
	if user, password, ok := r.BasicAuth(); f.user != "" && (!ok || user != f.user || password != f.password) {
		http.Error(w, "401 Unauthorized.", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     any               `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.ID == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	result, code, msg := f.rpcMethod(req.Method, req.Params)
	f.mu.Unlock()
	reply := map[string]any{"id": req.ID, "result": result, "error": nil}
	if code != 0 {
		reply["result"], reply["error"] = nil, map[string]any{"code": code, "message": msg}
	}
	_ = json.NewEncoder(w).Encode(reply)
}

func (f *fakeNode) rpcMethod(method string, params []json.RawMessage) (any, int, string) {
	arg := func(i int, v any) bool { return i < len(params) && json.Unmarshal(params[i], v) == nil }
	switch method {
	case "generatetoaddress":
		var n int
		var address string
		if !arg(0, &n) || !arg(1, &address) || n <= 0 {
			return nil, -8, "Invalid parameter"
		}
		addr, err := script.NewAddressFromString(address)
		if err != nil || address[0] == '1' {
			return nil, -5, "Error: Invalid address"
		}
		lock, _ := p2pkh.Lock(addr)
		return f.mine(n, lock), 0, ""
	case "getblockhash":
		var height int
		if !arg(0, &height) || height < 0 || height >= len(f.blocks) {
			return nil, -5, "Block not found"
		}
		return f.blocks[height].hash, 0, ""
	case "getblock":
		var hash string
		var verbosity int
		b := (*fakeBlock)(nil)
		if arg(0, &hash) && arg(1, &verbosity) && verbosity == 0 {
			b = f.block(hash)
		}
		if b == nil {
			return nil, -5, "Block not found"
		}
		return hex.EncodeToString(teranodeBlock(b.header, uint64(len(b.txs)), nil, b.txs[0].Bytes(), b.height)), 0, ""
	case "sendrawtransaction":
		var rawHex string
		if !arg(0, &rawHex) {
			return nil, -8, "Invalid parameter"
		}
		tx, err := transaction.NewTransactionFromHex(rawHex)
		if err != nil {
			return nil, -22, "TX decode failed"
		}
		if f.reject {
			return nil, -26, "TX rejected: txn-mempool-conflict"
		}
		for _, in := range tx.Inputs {
			op := fmt.Sprintf("%s:%d", in.SourceTXID, in.SourceTxOutIndex)
			if f.txs[in.SourceTXID.String()] == nil || f.spent[op] {
				return nil, -26, "TX rejected: missing or spent input " + op
			}
		}
		for _, in := range tx.Inputs {
			f.spent[fmt.Sprintf("%s:%d", in.SourceTXID, in.SourceTxOutIndex)] = true
		}
		f.txs[tx.TxID().String()] = tx
		f.mempool = append(f.mempool, tx)
		return tx.TxID().String(), 0, ""
	}
	return nil, -32601, "Method not found"
}

func (f *fakeNode) serveAsset(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.throttle > 0 {
		f.throttle--
		w.Header().Set("Retry-After", "0")
		writeJSONReply(w, http.StatusTooManyRequests, map[string]string{"message": "rate limit exceeded"})
		return
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/api/v1/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(p, "/")
	hash := ""
	if len(parts) > 1 {
		hash = parts[1]
	}
	switch {
	case p == "bestblockheader/json":
		writeJSONReply(w, http.StatusOK, f.headerJSON(f.blocks[len(f.blocks)-1]))
	case parts[0] == "header" && len(parts) == 3 && parts[2] == "json" && f.block(hash) != nil:
		writeJSONReply(w, http.StatusOK, f.headerJSON(f.block(hash)))
	case parts[0] == "block" && len(parts) == 3 && parts[2] == "json" && f.block(hash) != nil:
		b := f.block(hash)
		cb := b.txs[0]
		writeJSONReply(w, http.StatusOK, map[string]any{
			"header":            map[string]any{"version": 1, "hash_merkle_root": f.headerJSON(b)["merkleroot"], "bits": "207fffff"},
			"coinbase_tx":       map[string]any{"txid": cb.TxID().String(), "hex": cb.Hex(), "version": 1, "lockTime": 0},
			"transaction_count": len(b.txs),
			"height":            b.height,
		})
	case parts[0] == "utxos" && len(parts) == 3 && parts[2] == "json" && f.txs[hash] != nil:
		var us []map[string]any
		for i, o := range f.txs[hash].Outputs {
			status := "OK"
			if f.spent[fmt.Sprintf("%s:%d", hash, i)] {
				status = "SPENT"
			}
			us = append(us, map[string]any{"txid": hash, "vout": i, "lockingScript": o.LockingScript.String(), "satoshis": o.Satoshis, "status": status})
		}
		writeJSONReply(w, http.StatusOK, us)
	case parts[0] == "tx" && len(parts) == 2 && f.txs[hash] != nil:
		tx := f.txs[hash]
		data := tx.Bytes()
		if !tx.IsCoinbase() { // the UTXO store holds the Extended Format
			ext := tx.ShallowClone()
			for _, in := range ext.Inputs {
				in.SourceTransaction = f.txs[in.SourceTXID.String()]
			}
			data, _ = ext.EF()
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	case parts[0] == "txmeta" && len(parts) == 3 && parts[2] == "json" && f.txs[hash] != nil:
		meta := map[string]any{"blockIDs": []uint32{}, "blockHashes": []string{}, "blockHeights": []uint32{}, "isCoinbase": f.txs[hash].IsCoinbase()}
		if b := f.mined[hash]; b != nil {
			meta["blockIDs"], meta["blockHashes"], meta["blockHeights"] = []uint32{b.height}, []string{b.hash}, []uint32{b.height}
		}
		writeJSONReply(w, http.StatusOK, meta)
	case parts[0] == "merkle_proof" && len(parts) == 2 && f.txs[hash] != nil:
		b := f.mined[hash]
		if b == nil {
			writeJSONReply(w, http.StatusNotFound, map[string]string{"message": "mined transaction not found; BUMP proofs require a mined transaction ID"})
			return
		}
		var ids []*chainhash.Hash
		index := 0
		for i, tx := range b.txs {
			ids = append(ids, tx.TxID())
			if tx.TxID().String() == hash {
				index = i
			}
		}
		mp := proof(b.height, ids, index, true)
		if mp == nil {
			writeJSONReply(w, http.StatusInternalServerError, map[string]string{"message": "invalid BUMP format: BUMP path cannot be empty"})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(mp.Bytes())
	default:
		writeJSONReply(w, http.StatusNotFound, map[string]string{"message": "not found"})
	}
}

func (f *fakeNode) headerJSON(b *fakeBlock) map[string]any {
	var ids []*chainhash.Hash
	for _, tx := range b.txs {
		ids = append(ids, tx.TxID())
	}
	root := merkleRoot(ids).String()
	if f.badRoot {
		root = strings.Repeat("ab", 32)
	}
	return map[string]any{"hash": b.hash, "version": 1, "merkleroot": root, "height": b.height, "txCount": len(b.txs), "bits": "207fffff", "invalid": false}
}

func writeJSONReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fixture runs the command line and returns its exit code and output.
func fixture(env map[string]string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, func(k string) string { return env[k] }, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func readFixture[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// newKey runs keygen and returns the key file and its address.
func newKey(t *testing.T) (string, *script.Address) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.json")
	code, stdout, stderr := fixture(nil, "keygen", "-out", path)
	if code != 0 {
		t.Fatalf("keygen exit %d: %s", code, stderr)
	}
	_, addr, err := loadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != addr.AddressString+"\n" {
		t.Fatalf("keygen printed %q, want the address", stdout)
	}
	return path, addr
}

func TestUsage(t *testing.T) {
	tests := []struct {
		args []string
		code int
		want string
	}{
		{nil, 2, "Commands:"},
		{[]string{"-h"}, 0, "Commands:"},
		{[]string{"mint"}, 2, `unknown command "mint"`},
		{[]string{"ef", "-h"}, 0, "-broadcast"},
		{[]string{"fund", "-key"}, 2, "flag needs an argument"},
		{[]string{"beef", "-asset", "http://localhost", "-out", "x.json", "-txid", "ABC"}, 2, "64 hex digits"},
		{[]string{"fund", "-key", "k.json", "-rpc", "http://localhost", "extra"}, 2, `unexpected argument "extra"`},
	}
	for _, tc := range tests {
		code, _, stderr := fixture(nil, tc.args...)
		if code != tc.code || !strings.Contains(stderr, tc.want) {
			t.Errorf("fixture %q: exit %d, stderr %q; want exit %d with %q", tc.args, code, stderr, tc.code, tc.want)
		}
	}
}

func TestKeygen(t *testing.T) {
	path, _ := newKey(t)
	before, _ := os.ReadFile(path)
	var kf keyFile
	if err := json.Unmarshal(before, &kf); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v", st.Mode())
	}

	code, stdout, stderr := fixture(nil, "keygen", "-out", path)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "exists") || strings.Contains(stderr, kf.WIF) {
		t.Fatalf("keygen over a key: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("keygen replaced a key file")
	}
	if code, _, stderr := fixture(nil, "keygen"); code != 2 || !strings.Contains(stderr, "-out is required") {
		t.Fatalf("keygen without -out: exit %d, stderr %q", code, stderr)
	}
}

func TestFund(t *testing.T) {
	node := newFakeNode(t, "tpnil", "regtest-secret")
	keyPath, addr := newKey(t)
	env := map[string]string{"TPNIL_RPC_USER": "tpnil", "TPNIL_RPC_PASSWORD": "regtest-secret"}

	code, stdout, stderr := fixture(env, "fund", "-key", keyPath, "-rpc", node.rpc.URL, "-timeout", "30s")
	if code != 0 {
		t.Fatalf("fund exit %d: %s", code, stderr)
	}
	if node.height() != 101 {
		t.Fatalf("mined %d blocks, want 101", node.height())
	}
	node.mu.Lock()
	first := node.blocks[1].txs[0]
	node.mu.Unlock()
	if want := "blocks: 101\nfirst coinbase txid: " + first.TxID().String() + "\n"; stdout != want {
		t.Fatalf("fund printed %q, want %q", stdout, want)
	}
	if lock := lockTo(t, addr); len(pays(first, lock)) != 1 {
		t.Fatal("coinbase does not pay the key")
	}

	// Flags take precedence over the environment.
	code, stdout, stderr = fixture(env, "fund", "-key", keyPath, "-rpc", node.rpc.URL, "-rpc-password", "wrong-secret", "-blocks", "3")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "HTTP 401") || strings.Contains(stderr, "secret") {
		t.Fatalf("fund with a wrong password: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if node.height() != 101 {
		t.Fatal("a refused fund mined blocks")
	}
}

func TestEF(t *testing.T) {
	node := newFakeNode(t, "", "")
	keyPath, addr := newKey(t)
	lock := lockTo(t, addr)
	out := filepath.Join(t.TempDir(), "ef.json")

	// Height 50 is the newest coinbase that is mature at maturity 50 and
	// tip 99; it pays another key. Height 49 pays the key but is spent, and
	// the asset API throttles the first request.
	other := script.Script{script.OpTRUE}
	node.mu.Lock()
	node.mine(49, lock)
	node.mine(1, &other)
	node.mine(49, lock)
	node.spent[node.blocks[49].txs[0].TxID().String()+":0"] = true
	node.throttle = 1
	source := node.blocks[48].txs[0]
	node.mu.Unlock()

	code, stdout, stderr := fixture(nil, "ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL+"/api/v1/", "-out", out, "-maturity", "50", "-fee", "500")
	if code != 0 {
		t.Fatalf("ef exit %d: %s", code, stderr)
	}
	fx := readFixture[efFixture](t, out)
	if fx.Schema != efSchema {
		t.Fatalf("fixture schema %q", fx.Schema)
	}
	assertFourKeys(t, out)
	decoded, err := transaction.NewTransactionFromHex(fx.EFHex)
	if err != nil {
		t.Fatal(err)
	}
	in := decoded.Inputs[0]
	if in.SourceTXID.String() != source.TxID().String() || in.SourceTxOutIndex != 0 {
		t.Fatalf("fixture spends %s:%d, want %s:0", in.SourceTXID, in.SourceTxOutIndex, source.TxID())
	}
	if *in.SourceTxSatoshis() != 50_0000_0000 || !bytes.Equal(*in.SourceTxScript(), *lock) {
		t.Fatal("EF does not carry the coinbase output it spends")
	}
	if decoded.TxID().String() != fx.TxID || decoded.Hex() != fx.RawHex {
		t.Fatal("efHex, rawHex and txid disagree")
	}
	want := fmt.Sprintf("spent: %s:0 (coinbase at height 48)\nfee: 96 satoshis\ntxid: %s\n", source.TxID(), fx.TxID)
	if stdout != want || node.mempoolSize() != 0 {
		t.Fatalf("ef printed %q (mempool %d), want %q", stdout, node.mempoolSize(), want)
	}

	// With -broadcast the node accepts the transaction, and a second
	// fixture cannot spend the same output.
	code, stdout, stderr = fixture(nil, "ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL, "-out", out, "-maturity", "50", "-broadcast")
	if code != 0 || !strings.HasSuffix(stdout, "broadcast: accepted\n") || node.mempoolSize() != 1 {
		t.Fatalf("ef -broadcast: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	node.mu.Lock()
	held := node.mempool[0].TxID().String()
	node.mu.Unlock()
	if fx2 := readFixture[efFixture](t, out); held != fx2.TxID {
		t.Fatal("broadcast fixture is not the transaction the node holds")
	}
	code, _, stderr = fixture(nil, "ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL, "-out", out, "-maturity", "50", "-scan", "3")
	if code != 1 || !strings.Contains(stderr, "no unspent coinbase output pays "+addr.AddressString+" at heights 48 to 50") {
		t.Fatalf("ef after the spends: exit %d, stderr %q", code, stderr)
	}

	// A transaction the node rejects leaves no fixture.
	node.mu.Lock()
	node.reject = true
	node.mu.Unlock()
	rejected := filepath.Join(t.TempDir(), "rejected.json")
	code, stdout, stderr = fixture(nil, "ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL, "-out", rejected, "-maturity", "50", "-broadcast")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "RPC sendrawtransaction: error -26: TX rejected") {
		t.Fatalf("ef -broadcast of a rejected transaction: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("ef wrote the fixture of a rejected transaction")
	}
}

func TestEFImmature(t *testing.T) {
	node := newFakeNode(t, "", "")
	keyPath, addr := newKey(t)
	node.mu.Lock()
	node.mine(99, lockTo(t, addr))
	node.mu.Unlock()
	out := filepath.Join(t.TempDir(), "ef.json")
	code, stdout, stderr := fixture(nil, "ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL, "-out", out)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "no coinbase is mature at chain height 99 with maturity 100") {
		t.Fatalf("ef on an immature chain: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("ef wrote a fixture after failing")
	}
}

// TestSession runs the README session: keygen, fund, ef -broadcast, a block
// that mines the transaction, and beef.
func TestSession(t *testing.T) {
	node := newFakeNode(t, "tpnil", "regtest-secret")
	env := map[string]string{"TPNIL_RPC_USER": "tpnil", "TPNIL_RPC_PASSWORD": "regtest-secret"}
	keyPath, _ := newKey(t)
	dir := t.TempDir()
	efOut, beefOut := filepath.Join(dir, "ef.json"), filepath.Join(dir, "beef.json")
	steps := [][]string{
		{"fund", "-key", keyPath, "-rpc", node.rpc.URL},
		{"ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL, "-out", efOut, "-broadcast"},
		{"fund", "-key", keyPath, "-rpc", node.rpc.URL, "-blocks", "1"},
	}
	for _, args := range steps {
		if code, _, stderr := fixture(env, args...); code != 0 {
			t.Fatalf("%s exit %d: %s", args[0], code, stderr)
		}
	}
	efx := readFixture[efFixture](t, efOut)
	node.mu.Lock()
	block := node.blocks[102]
	mined := node.mined[efx.TxID] == block
	node.mu.Unlock()
	if !mined {
		t.Fatal("the fixture transaction was not mined in block 102")
	}

	code, stdout, stderr := fixture(nil, "beef", "-asset", node.asset.URL, "-txid", strings.ToUpper(efx.TxID), "-out", beefOut)
	if code != 0 {
		t.Fatalf("beef exit %d: %s", code, stderr)
	}
	bfx := readFixture[beefFixture](t, beefOut)
	if bfx.Schema != beefSchema || bfx.TxID != efx.TxID || bfx.BlockHash != block.hash || bfx.BlockHeight != 102 {
		t.Fatalf("beef fixture %+v, want %s in block %s at 102", bfx, efx.TxID, block.hash)
	}
	root := merkleRoot([]*chainhash.Hash{block.txs[0].TxID(), block.txs[1].TxID()}).String()
	if want := fmt.Sprintf("txid: %s\nblock: %s (height 102)\nmerkle root: %s\n", efx.TxID, block.hash, root); stdout != want {
		t.Fatalf("beef printed %q, want %q", stdout, want)
	}
	b, _ := hex.DecodeString(bfx.BEEFHex)
	parsed, subject, err := transaction.NewBeefFromAtomicBytes(b)
	if err != nil || subject.String() != efx.TxID {
		t.Fatalf("Atomic BEEF subject %v: %v", subject, err)
	}
	if tx := parsed.FindTransactionByHash(subject); tx == nil || tx.Hex() != efx.RawHex {
		t.Fatal("Atomic BEEF does not carry the fixture transaction")
	}
	tracker := &headerTracker{root: root, height: 102}
	if ok, err := parsed.Verify(context.Background(), tracker, false); !ok || err != nil || !tracker.checked {
		t.Fatalf("Atomic BEEF does not verify against block 102: %v %v", ok, err)
	}
}

func TestBEEFErrors(t *testing.T) {
	node := newFakeNode(t, "", "")
	keyPath, addr := newKey(t)
	node.mu.Lock()
	node.mine(101, lockTo(t, addr))
	coinbase := node.blocks[1].txs[0].TxID().String()
	node.mu.Unlock()
	dir := t.TempDir()
	efOut := filepath.Join(dir, "ef.json")
	if code, _, stderr := fixture(nil, "ef", "-key", keyPath, "-rpc", node.rpc.URL, "-asset", node.asset.URL, "-out", efOut, "-broadcast"); code != 0 {
		t.Fatalf("ef exit %d: %s", code, stderr)
	}
	txid := readFixture[efFixture](t, efOut).TxID

	tests := []struct {
		name    string
		mine    bool
		badRoot bool
		txid    string
		want    string
	}{
		{name: "unmined", txid: txid, want: "HTTP 404"},
		{name: "header disagrees", mine: true, badRoot: true, txid: txid, want: "has the merkle root"},
		{name: "unknown transaction", txid: strings.Repeat("cd", 32), want: "HTTP 404"},
		{name: "coinbase alone in its block", txid: coinbase, want: "BUMP path cannot be empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node.mu.Lock()
			if tc.mine && node.mined[txid] == nil {
				node.mine(1, lockTo(t, addr))
			}
			node.badRoot = tc.badRoot
			node.mu.Unlock()
			out := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".json")
			code, stdout, stderr := fixture(nil, "beef", "-asset", node.asset.URL, "-txid", tc.txid, "-out", out)
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("beef: exit %d, stdout %q, stderr %q; want %q", code, stdout, stderr, tc.want)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("beef wrote a fixture after failing")
			}
		})
	}
}

// assertFourKeys checks the EF fixture file carries exactly the four fields its
// consumers read; some of them decode it with unknown fields disallowed.
func assertFourKeys(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "efHex,rawHex,schema,txid" {
		t.Fatalf("fixture keys %v, want efHex rawHex schema txid", keys)
	}
}
