package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ysh0566/ponder-go/abi"
	"github.com/ysh0566/ponder-go/rpc"
)

func TestCallbackCCIPRead(t *testing.T) {
	const target = "0x000000000000000000000000000000000000cc1a"
	const avatar = "data:image/png;base64,aWNvbg=="
	raw, err := os.ReadFile("../testdata/indexing/ccip/GatewayResolver.json")
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		ABI  json.RawMessage
		Code string
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	parsed, err := abi.Parse(string(artifact.ABI))
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			var hits atomic.Int32
			waiting := make(chan struct{})
			word := fmt.Sprintf("0x%064x", 42)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				switch r.URL.Path {
				case "/bad":
					http.Error(w, "unavailable", 503)
				case "/malformed":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"data":"bad"}`)
				case "/get/" + target + "/0x12":
					if r.Method != http.MethodGet {
						t.Errorf("GET template received %s", r.Method)
					}
					fmt.Fprint(w, word)
				case "/post":
					var payload map[string]string
					if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload["sender"] != common.HexToAddress(target).Hex() || payload["data"] != "0x12" {
						t.Errorf("invalid gateway request %v %v", r.Method, payload)
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(Object{"data": word})
				case "/wait":
					// Read the POST body before waiting for the client disconnect.
					var payload Object
					_ = json.NewDecoder(r.Body).Decode(&payload)
					close(waiting)
					<-r.Context().Done()
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			net := newSourceClientChain(t, sourceChainContracts{})
			var unused any
			net.call("anvil_setCode", []any{target, artifact.Code}, &unused)
			call := func(fn string, args ...any) ContractCall {
				return ContractCall{Address: target, ABI: parsed, FunctionName: fn, Args: args}
			}
			encode := func(fn string, args ...any) string {
				data, _, err := encodeContractCall(call(fn, args...))
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			net.send(target, encode("setup", server.URL))
			net.send(target, encode("setOffset", big.NewInt(5)))
			chain := Object{"name": "local", "id": 1, "cacheRpcRequests": true, "viemChain": Object{"contracts": Object{"ensUniversalResolver": Object{"address": target}}}}
			clients, err := NewClientSet([]Object{chain}, []rpc.Requester{net.rpc}, adapterStore(t, dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer clients.Close(context.Background())
			runClientCallback(t, ctx, dialect, chain, clients, func(ctx context.Context, args *HandlerArgs) error {
				c := args.Context.Client
				cc := CCIPReadOptions{HTTPClient: server.Client()}
				opt := CallOptions{CCIPRead: cc}
				tx := func(mode int) Object { return Object{"to": target, "data": encode("value", mode)} }
				for _, mode := range []int{0, 1, 7} {
					data, err := c.Call(ctx, tx(mode), opt)
					if err != nil || data == nil || *data != fmt.Sprintf("0x%064x", 47) {
						return fmt.Errorf("value(%d): %v, %v", mode, ensTestValue(data), err)
					}
				}
				value, err := c.ReadContract(ctx, call("value", 0), ReadContractOptions{CCIPRead: cc})
				if err != nil || fmt.Sprint(value) != "47" {
					return fmt.Errorf("readContract %v, %v", value, err)
				}
				sim, err := c.SimulateContract(ctx, call("value", 1), SimulateContractOptions{CallOptions: opt})
				if err != nil || sim == nil || fmt.Sprint(sim.Result) != "47" {
					return fmt.Errorf("simulateContract %v, %v", sim, err)
				}
				start := hits.Load()
				var revert *rpc.Error
				if _, err := c.Call(ctx, tx(1), CallOptions{CCIPRead: CCIPReadOptions{Disabled: true, HTTPClient: server.Client()}}); !errors.As(err, &revert) {
					return fmt.Errorf("disabled: %v", err)
				}
				if _, err := c.Request(ctx, "eth_call", []any{tx(1), "0x1"}); !errors.As(err, &revert) {
					return fmt.Errorf("raw request: %v", err)
				}
				if _, err := c.Call(ctx, tx(2), opt); err == nil || !strings.Contains(err.Error(), "differs from target") {
					return fmt.Errorf("sender mismatch: %v", err)
				}
				if _, err := c.Call(ctx, tx(5), opt); !errors.As(err, &revert) {
					return fmt.Errorf("ordinary revert: %v", err)
				}
				if hits.Load() != start {
					return fmt.Errorf("unexpected HTTP request before sender verification or for a raw call")
				}
				for _, mode := range []int{3, 6, 8} {
					if _, err := c.Call(ctx, tx(mode), opt); err == nil {
						return fmt.Errorf("value(%d) unexpectedly resolved", mode)
					}
				}
				ens := ENSNameOptions{CCIPRead: cc}
				address, err := c.GetEnsAddress(ctx, "alice.eth", ens)
				if err != nil || address == nil || *address != common.HexToAddress("0xabc").Hex() {
					return fmt.Errorf("batch address: %v, %v", ensTestValue(address), err)
				}
				text, err := c.GetEnsText(ctx, "alice.eth", "avatar", ens)
				if err != nil || text == nil || *text != avatar {
					return fmt.Errorf("batch text: %v, %v", ensTestValue(text), err)
				}
				image, err := c.GetEnsAvatar(ctx, "alice.eth", ENSAvatarOptions{ENSNameOptions: ens})
				if err != nil || image == nil || *image != avatar {
					return fmt.Errorf("batch avatar: %v, %v", ensTestValue(image), err)
				}
				name, err := c.GetEnsName(ctx, target, ens)
				if err != nil || name == nil || *name != "alice.eth" {
					return fmt.Errorf("offchain reverse: %v, %v", ensTestValue(name), err)
				}
				resolver, err := c.GetEnsResolver(ctx, "alice.eth", ENSResolverOptions{CCIPRead: cc})
				if err != nil || resolver != common.HexToAddress(target).Hex() {
					return fmt.Errorf("offchain resolver: %v, %v", resolver, err)
				}
				fail := false
				rows, err := c.Multicall(ctx, []ContractCall{call("value", 1)}, MulticallOptions{MulticallAddress: target, Account: target, CCIPRead: cc, AllowFailure: &fail})
				if err != nil || len(rows) != 1 || fmt.Sprint(rows[0]) != "42" {
					return fmt.Errorf("offchain aggregate: %v, %v", rows, err)
				}
				reqCtx, stop := context.WithCancel(ctx)
				defer stop()
				done := make(chan error, 1)
				go func() { _, err := c.Call(reqCtx, tx(4), opt); done <- err }()
				select {
				case <-waiting:
				case <-ctx.Done():
					return ctx.Err()
				}
				stop()
				if err := <-done; !errors.Is(err, context.Canceled) {
					return fmt.Errorf("cancelled gateway: %v", err)
				}
				return nil
			})
		})
	}
}

func TestCCIPRevertEnvelope(t *testing.T) {
	for _, data := range []string{`"0x556f1830"`, `{"data":"0x556f1830"}`} {
		if got := ccipRevert(fmt.Errorf("wrapped: %w", &rpc.Error{Data: json.RawMessage(data)})); got != "0x556f1830" {
			t.Fatal(got)
		}
	}
	s := ccipSession{}
	if _, err := s.batch(t.Context(), []byte{0, 0}, 0); err == nil {
		t.Fatal("accepted malformed local batch")
	}
}

func TestCCIPGatewayLimits(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "0x12")
	}))
	defer server.Close()
	s := ccipSession{http: server.Client(), active: make(chan struct{}, 4)}
	s.bodyBytes.Store(maxCCIPBytes - 1)
	q := ccipQuery{Urls: []string{server.URL}}
	if _, err := s.query(t.Context(), q, 0); err == nil || hits.Load() != 1 {
		t.Fatalf("cumulative byte limit: %d, %v", hits.Load(), err)
	}
	if _, err := s.query(t.Context(), q, 0); err == nil || hits.Load() != 1 {
		t.Fatalf("issued another request after byte limit: %d, %v", hits.Load(), err)
	}
	s.requests.Store(64)
	if _, err := s.query(t.Context(), q, 0); err == nil || !strings.Contains(err.Error(), "request limit") {
		t.Fatal(err)
	}
	if _, err := s.query(t.Context(), q, 8); err == nil || !strings.Contains(err.Error(), "nesting limit") {
		t.Fatal(err)
	}
}
