// Command header-anchor serves the header routes an overlay bridge's header
// store and a chain tracker read, from a Teranode node's asset API:
//
//	GET /v1/tip             the node's best block header
//	GET /v1/header/{hash}   the header of a block by hash
//	GET /v1/root/{height}   the header of the block at a height (height 1 and up)
//
// Each answers {"height", "hash", "merkleRoot"}. GET /healthz answers 200 while
// the asset API answers.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func main() {
	fs := flag.NewFlagSet("header-anchor", flag.ExitOnError)
	listen := fs.String("listen", ":9188", "HTTP listen address")
	asset := fs.String("asset", "", "Teranode asset API base URL including /api/v1, for example http://node.example:8090/api/v1 (required)")
	_ = fs.Parse(os.Args[1:])
	if *asset == "" {
		fmt.Fprintln(os.Stderr, "header-anchor: -asset is required")
		os.Exit(2)
	}
	a := &assetAPI{base: strings.TrimRight(*asset, "/"), http: &http.Client{Timeout: 10 * time.Second}}
	srv := &http.Server{Addr: *listen, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	slog.Info("header-anchor starting", "listen", *listen, "asset", a.base)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "header-anchor:", err)
		os.Exit(1)
	}
}

// header is the answer of every route.
type header struct {
	Height     uint32 `json:"height"`
	Hash       string `json:"hash"`
	MerkleRoot string `json:"merkleRoot"`
}

type assetAPI struct {
	base string
	http *http.Client
}

// assetHeader is the asset API's header JSON (/bestblockheader/json,
// /header/{hash}/json).
type assetHeader struct {
	Height     uint32 `json:"height"`
	Hash       string `json:"hash"`
	MerkleRoot string `json:"merkleroot"`
}

func (a *assetAPI) getJSON(ctx context.Context, path string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(v)
}

func (a *assetAPI) headerAt(ctx context.Context, path string) (header, int, error) {
	var h assetHeader
	code, err := a.getJSON(ctx, path, &h)
	if err != nil {
		return header{}, code, err
	}
	if !hashRE.MatchString(h.Hash) || !hashRE.MatchString(h.MerkleRoot) {
		return header{}, http.StatusBadGateway, fmt.Errorf("%s: malformed header", path)
	}
	return header(h), http.StatusOK, nil
}

// hashAt resolves the hash of the block at height (1 and up): the asset API's
// block-by-height JSON names the next block's hash, so block h-1 names block h.
func (a *assetAPI) hashAt(ctx context.Context, height uint64) (string, int, error) {
	var b struct {
		NextBlock string `json:"nextblock"`
	}
	code, err := a.getJSON(ctx, fmt.Sprintf("/block/height/%d/json", height-1), &b)
	if err != nil {
		return "", code, err
	}
	if !hashRE.MatchString(b.NextBlock) {
		return "", http.StatusNotFound, fmt.Errorf("no block at height %d", height)
	}
	return b.NextBlock, http.StatusOK, nil
}

func (a *assetAPI) routes() http.Handler {
	mux := http.NewServeMux()
	answer := func(w http.ResponseWriter, h header, code int, err error) {
		if err != nil {
			if code == 0 || code == http.StatusOK {
				code = http.StatusBadGateway
			}
			if code >= 500 {
				code = http.StatusBadGateway
			}
			http.Error(w, err.Error(), code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h)
	}
	mux.HandleFunc("GET /v1/tip", func(w http.ResponseWriter, r *http.Request) {
		h, code, err := a.headerAt(r.Context(), "/bestblockheader/json")
		answer(w, h, code, err)
	})
	mux.HandleFunc("GET /v1/header/{hash}", func(w http.ResponseWriter, r *http.Request) {
		hash := strings.ToLower(r.PathValue("hash"))
		if !hashRE.MatchString(hash) {
			http.Error(w, "hash must be 64 hex digits", http.StatusBadRequest)
			return
		}
		h, code, err := a.headerAt(r.Context(), "/header/"+hash+"/json")
		answer(w, h, code, err)
	})
	mux.HandleFunc("GET /v1/root/{height}", func(w http.ResponseWriter, r *http.Request) {
		height, err := strconv.ParseUint(r.PathValue("height"), 10, 32)
		if err != nil || height == 0 {
			http.Error(w, "height must be a block height of 1 or more", http.StatusBadRequest)
			return
		}
		hash, code, err := a.hashAt(r.Context(), height)
		if err != nil {
			answer(w, header{}, code, err)
			return
		}
		h, code, err := a.headerAt(r.Context(), "/header/"+hash+"/json")
		if err == nil && uint64(h.Height) != height {
			err, code = fmt.Errorf("header %s is at height %d, not %d", hash, h.Height, height), http.StatusBadGateway
		}
		answer(w, h, code, err)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		var h assetHeader
		if _, err := a.getJSON(r.Context(), "/bestblockheader/json", &h); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}
