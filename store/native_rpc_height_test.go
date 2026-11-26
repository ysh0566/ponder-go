package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
)

func TestNativeRPCCacheHeight(t *testing.T) {
	for _, tc := range []struct{ method, params, want string }{
		{"eth_getBlockByNumber", `["0x0",false]`, "0"},
		{"eth_getBalance", `["0xabc","0x7fffffffffffffff"]`, "9223372036854775807"},
		{"eth_feeHistory", `["0x5","0xa",[]]`, "10"},
		{"eth_getStorageAt", `["0xabc","0x2","0x3"]`, "3"},
		{"eth_call", `[{},{"blockNumber":"0x5"}]`, "5"},
		{"eth_getProof", `["0xabc",[],{"blockHash":"0xabc"}]`, ""},
		{"eth_getBalance", `["0xabc",{"blockNumber":"0x0","blockHash":"0xabc"}]`, ""},
		{"eth_getLogs", `[{"fromBlock":"0x2","toBlock":"0xa"}]`, "10"},
		{"eth_getLogs", `[{"fromBlock":"0xa","toBlock":"0x2"}]`, ""},
		{"eth_getLogs", `[{"toBlock":"0xa"}]`, ""},
		{"eth_getLogs", `[{"fromBlock":"0x0","toBlock":"latest"}]`, ""},
		{"eth_getLogs", `[{"fromBlock":"0x0","toBlock":"0x2","blockHash":"0xabc"}]`, ""},
		{"eth_getBlockByNumber", `["0x8000000000000000",false]`, ""},
		{"eth_getBlockByNumber", `["0x01",false]`, ""},
		{"eth_getBlockByNumber", `["0xZ",false]`, ""},
		{"eth_getBlockByNumber", `["latest",false]`, ""},
		{"eth_getBlockByNumber", `["pending",false]`, ""},
		{"eth_getBlockByHash", `["0x1",false]`, ""},
		{"eth_getBalance", `["0xabc"]`, ""},
		{"eth_getCode", `{"blockNumber":"0x2"}`, ""},
		{"eth_custom", `["0x2"]`, ""},
	} {
		key, err := parseNativeRPCKey(fmt.Sprintf(`[1,%q,%s]`, tc.method, tc.params))
		if err != nil {
			t.Fatal(err)
		}
		height := key.blockNumber()
		if (tc.want == "" && height != nil) || (tc.want != "" && height != tc.want) {
			t.Errorf("%s %s: height %v, want %q", tc.method, tc.params, height, tc.want)
		}
	}
}

func TestNativeRPCCacheRewind(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			b := sourceBackend(t, ctx, dialect)
			s, err := New(ctx, b, sourceActionsModel(), fmt.Sprintf("ponder_go_rpc_rewind_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := s.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// Return a different result on every miss, including after the fork.
			calls := 0
			client, err := rpc.NewSourceTransportClient(1, func(context.Context, string, any) (json.RawMessage, error) {
				calls++
				return json.Marshal(fmt.Sprintf("0x%x", calls))
			})
			if err != nil {
				t.Fatal(err)
			}
			client.ChainID = 1
			const address = "0x0000000000000000000000000000000000000abc"
			get := func(c *rpc.Client, selector any, want int) error {
				var value string
				if err := c.CallCached(ctx, "eth_getBalance", []any{address, selector}, &value); err != nil {
					return err
				}
				if expected := fmt.Sprintf("0x%x", want); value != expected {
					return fmt.Errorf("balance at %v returned %s, want %s", selector, value, expected)
				}
				return nil
			}
			// Seed genesis outside a handler and the fork boundary inside one.
			if err := get(client.WithCache(cache), "0x0", 1); err != nil {
				t.Fatal(err)
			}
			// Database and bridge must preserve integers outside the JSON safe range.
			large := `[1,"eth_getBlockByNumber",["0x20000000000001",false]]`
			if err := cache.CachePut(ctx, large, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			key, err := parseNativeRPCKey(large)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := b.Query(ctx, "SELECT block_number::text AS height FROM "+cache.table("rpc_request_results")+" WHERE chain_id=1 AND request_hash=$1", key.hash)
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["height"] != "9007199254740993" {
				t.Fatalf("stored height %v: %v", rows.Rows, err)
			}
			unaffected := `[3,"eth_getBalance",["0xabc","0x9"]]`
			if err := cache.CachePut(ctx, unaffected, json.RawMessage("25")); err != nil {
				t.Fatal(err)
			}
			applyReads := func(id string, expected []int) error {
				return s.Apply(ctx, event(id, 1, 3), func(w *Writer) error {
					view := client.WithCache(w.EventRPCCache())
					selectors := []any{"0x0", "0x2", "0x3", map[string]string{"blockHash": fmt.Sprintf("0x%064x", 42)}}
					for i, selector := range selectors {
						if err := get(view, selector, expected[i]); err != nil {
							return err
						}
					}
					return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "10"}, Insert)
				})
			}
			if err := applyReads("original", []int{1, 2, 3, 4}); err != nil {
				t.Fatal(err)
			}
			// This peer event also needs replay because it wrote after the origin.
			peerSafe := `[2,"eth_getBalance",["0xabc","0x3"]]`
			peerReplay := `[2,"eth_getBalance",["0xabc","0x4"]]`
			err = s.Apply(ctx, event("dependent", 2, 4), func(w *Writer) error {
				view := w.EventRPCCache()
				for _, key := range []string{peerSafe, peerReplay} {
					if err := view.CachePut(ctx, key, json.RawMessage("42")); err != nil {
						return err
					}
				}
				return w.Write(ctx, "account", Row{"address": sourceZeroAddress, "balance": "20"}, Update)
			})
			if err != nil {
				t.Fatal(err)
			}
			canceled := errors.New("rollback callback")
			err = s.Apply(ctx, event("aborted", 1, 4), func(w *Writer) error {
				if err := get(client.WithCache(w.EventRPCCache()), "0x1", 5); err != nil {
					return err
				}
				return canceled
			})
			if !errors.Is(err, canceled) {
				t.Fatal(err)
			}
			if _, ok, err := cache.CacheGet(ctx, fmt.Sprintf(`[1,"eth_getBalance",[%q,"0x1"]]`, address)); err != nil || ok {
				t.Fatalf("aborted callback cached a result: hit %t, error %v", ok, err)
			}
			if err := s.RevertAndRewind(ctx, checkpoint.Block(2, 1, 2, true)); err != nil {
				t.Fatal(err)
			}
			if err := applyReads("replacement", []int{1, 2, 6, 7}); err != nil {
				t.Fatal(err)
			}
			if _, hit, err := cache.CacheGet(ctx, peerReplay); err != nil || hit {
				t.Fatalf("replayed peer retained block 4: hit %t, error %v", hit, err)
			}
			value, hit, err := cache.CacheGet(ctx, peerSafe)
			if err != nil || !hit || string(value) != "42" {
				t.Fatalf("peer lost its own block 3 boundary: %s, hit %t, error %v", value, hit, err)
			}
			value, hit, err = cache.CacheGet(ctx, unaffected)
			if err != nil || !hit || string(value) != "25" || calls != 7 {
				t.Fatalf("unaffected chain result %s, hit %t, calls %d, error %v", value, hit, calls, err)
			}
		})
	}
}
