// Command overlay-host is a small overlay host for the TPNIL joined
// configuration. It runs the go-overlay-services engine, storage and HTTP
// routes as published, unmodified, with:
//
//   - one topic manager that admits every output of a submitted transaction;
//   - one lookup service that answers a query by outpoint;
//   - a chain tracker that reads a header service's /v1/tip and
//     /v1/root/{height} routes;
//   - the go-overlay-services MongoDB store, which needs a replica set.
//
// BRC-22 /submit and BRC-24 /lookup are mounted at the root.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/bsv-blockchain/go-overlay-services/pkg/storage/mongodb"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "overlay-host:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("overlay-host", flag.ContinueOnError)
	listen := fs.String("listen", ":8080", "HTTP listen address for /submit, /lookup and the other go-overlay-services routes")
	topic := fs.String("topic", "", "topic manager name, tm_ followed by the name (required)")
	service := fs.String("lookup-service", "", "lookup service name, ls_ followed by the name (required)")
	mongoURI := fs.String("mongo-uri", os.Getenv("OVERLAY_HOST_MONGO_URI"), "MongoDB connection URI of a replica set (required; env OVERLAY_HOST_MONGO_URI)")
	database := fs.String("database", "", "MongoDB database for this host's store (required)")
	network := fs.String("network", "", "chain network the store is scoped to, for example regtest (required)")
	genesis := fs.String("genesis-hash", "", "genesis block hash of that network (required)")
	nodeID := fs.String("node-id", "", "node identity the store is scoped to (default: the host name)")
	headers := fs.String("headers", "", "header service base URL serving /v1/tip and /v1/root/{height}, the chain tracker (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, r := range []struct{ name, v string }{
		{"topic", *topic}, {"lookup-service", *service}, {"mongo-uri", *mongoURI}, {"database", *database},
		{"network", *network}, {"genesis-hash", *genesis}, {"headers", *headers},
	} {
		if r.v == "" {
			return fmt.Errorf("-%s is required", r.name)
		}
	}
	if !strings.HasPrefix(*topic, "tm_") || !strings.HasPrefix(*service, "ls_") {
		return errors.New("-topic must start with tm_ and -lookup-service with ls_")
	}
	host, portText, err := net.SplitHostPort(*listen)
	if err != nil {
		return fmt.Errorf("-listen: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("-listen port: %w", err)
	}
	if *nodeID == "" {
		if *nodeID, err = os.Hostname(); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	store, err := mongodb.Connect(ctx, *mongoURI, mongodb.Config{
		Database: *database,
		Scope:    engine.StorageScope{Network: *network, GenesisHash: *genesis, NodeID: *nodeID},
	})
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	eng := engine.NewEngine(&engine.Config{
		Managers:       map[string]engine.TopicManager{*topic: admitAll{name: *topic}},
		LookupServices: map[string]engine.LookupService{*service: byOutpoint{name: *service}},
		Storage:        store,
		ChainTracker:   &headerTracker{base: strings.TrimRight(*headers, "/"), http: &http.Client{Timeout: 10 * time.Second}},
	})
	srv := server.New(server.WithEngine(eng), server.WithConfig(server.Config{
		AppName:               "overlay-host",
		Addr:                  host,
		Port:                  port,
		ServerHeader:          "overlay-host",
		AdminBearerToken:      randomToken(),
		OctetStreamLimit:      64 << 20,
		ConnectionReadTimeout: 30 * time.Second,
		ARCCallbackToken:      randomToken(),
		BaseURL:               "",
	}))
	log.Info("overlay-host starting", "listen", *listen, "topic", *topic, "lookup_service", *service,
		"database", *database, "network", *network, "headers", *headers)

	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe(ctx) }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shut)
	}
}

// randomToken is a fresh secret for the go-overlay-services administrative and
// callback routes, which this host does not use; it is never printed.
func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// admitAll is a topic manager that admits every output of a submitted
// transaction and retains none of the coins it spends.
type admitAll struct{ name string }

func (admitAll) IdentifyAdmissibleOutputs(_ context.Context, beef *transaction.Beef, txid *chainhash.Hash, _ []uint32) (overlay.AdmittanceInstructions, error) {
	tx := beef.FindTransactionByHash(txid)
	if tx == nil {
		return overlay.AdmittanceInstructions{}, fmt.Errorf("transaction %s is not in the submitted BEEF", txid)
	}
	outs := make([]uint32, len(tx.Outputs))
	for i := range tx.Outputs {
		outs[i] = uint32(i)
	}
	return overlay.AdmittanceInstructions{OutputsToAdmit: outs, CoinsToRetain: []uint32{}}, nil
}

func (admitAll) IdentifyNeededInputs(context.Context, *transaction.Beef, *chainhash.Hash) ([]*transaction.Outpoint, error) {
	return nil, nil
}

func (admitAll) GetDocumentation() string {
	return "Admits every output of a submitted transaction."
}

func (a admitAll) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: a.name, Description: "Admits every output of a submitted transaction."}
}

// byOutpoint is a lookup service that answers {"outpoint": "<txid>.<index>"}
// with that output, when the host holds it. It returns a formula; the engine
// loads the output and its BEEF from storage.
type byOutpoint struct{ name string }

func (byOutpoint) OutputAdmittedByTopic(context.Context, *engine.OutputAdmittedByTopic) error {
	return nil
}
func (byOutpoint) OutputSpent(context.Context, *engine.OutputSpent) error { return nil }
func (byOutpoint) OutputNoLongerRetainedInHistory(context.Context, *transaction.Outpoint, string) error {
	return nil
}
func (byOutpoint) OutputEvicted(context.Context, *transaction.Outpoint) error { return nil }
func (byOutpoint) OutputBlockHeightUpdated(context.Context, *chainhash.Hash, uint32, uint64) error {
	return nil
}

func (byOutpoint) Lookup(_ context.Context, q *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	var query struct {
		Outpoint string `json:"outpoint"`
	}
	if err := json.Unmarshal(q.Query, &query); err != nil || query.Outpoint == "" {
		return nil, errors.New(`query must be {"outpoint": "<txid>.<index>"}`)
	}
	op, err := transaction.OutpointFromString(query.Outpoint)
	if err != nil {
		return nil, fmt.Errorf("outpoint %q: %w", query.Outpoint, err)
	}
	return &lookup.LookupAnswer{Type: lookup.AnswerTypeFormula, Formulas: []lookup.LookupFormula{{Outpoint: op}}}, nil
}

func (byOutpoint) GetDocumentation() string {
	return `Answers {"outpoint": "<txid>.<index>"} with that output when this host holds it.`
}

func (b byOutpoint) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{Name: b.name, Description: "Looks an output up by outpoint."}
}

// headerTracker is a chain tracker backed by a header service's /v1 routes.
type headerTracker struct {
	base string
	http *http.Client
}

type headerBody struct {
	Height     uint32 `json:"height"`
	Hash       string `json:"hash"`
	MerkleRoot string `json:"merkleRoot"`
}

func (h *headerTracker) get(ctx context.Context, path string) (headerBody, error) {
	var b headerBody
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+path, nil)
	if err != nil {
		return b, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return b, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return b, fmt.Errorf("%s%s: HTTP %d", h.base, path, resp.StatusCode)
	}
	return b, json.NewDecoder(resp.Body).Decode(&b)
}

// IsValidRootForHeight reports whether root is the merkle root of the block at
// height on the header service's chain.
func (h *headerTracker) IsValidRootForHeight(ctx context.Context, root *chainhash.Hash, height uint32) (bool, error) {
	b, err := h.get(ctx, fmt.Sprintf("/v1/root/%d", height))
	if err != nil {
		return false, err
	}
	return strings.EqualFold(b.MerkleRoot, root.String()), nil
}

// CurrentHeight is the header service's tip height.
func (h *headerTracker) CurrentHeight(ctx context.Context) (uint32, error) {
	b, err := h.get(ctx, "/v1/tip")
	if err != nil {
		return 0, err
	}
	return b.Height, nil
}
