package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	protocol "github.com/ysh0566/ponder-go/events"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func handoffFixture(t *testing.T, dialect string) SourceHistoricalCoordinatorOptions {
	t.Helper()
	file, err := os.Open("../testdata/runtime/source-runtime-multichain-probes.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fixture struct {
		Backends map[string][]json.RawMessage `json:"backend_cases"`
	}
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	source := handoffStore(t, dialect)
	for _, raw := range fixture.Backends[dialect] {
		var header struct{ Name string }
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		if header.Name == "two-chain-pending" {
			var entry map[string]any
			selected := json.NewDecoder(bytes.NewReader(raw))
			selected.UseNumber()
			if err := selected.Decode(&entry); err != nil {
				t.Fatal(err)
			}
			call, err := sourceMultichainProbeCall(entry)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := newSourceMultichainReplay(newSourceLiveRegistry(), call, source)
			if err != nil {
				t.Fatal(err)
			}
			return replay.options
		}
	}
	t.Fatal("missing two-chain pending fixture")
	return SourceHistoricalCoordinatorOptions{}
}

func handoffStore(t *testing.T, dialect string) *store.SourceSyncStore {
	t.Helper()
	ctx := t.Context()
	// The PGlite subprocess must remain alive through schema cleanup.
	backend := stagedBackend(t, context.Background(), dialect, "memory://")
	t.Cleanup(func() { backend.Close() })
	table := schema.Table{Name: "probe", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}
	db, err := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{table}}, fmt.Sprintf("ponder_go_handoff_%x", time.Now().UnixNano()), schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := db.EnableProtocolCache(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cache.Close()
		for _, ns := range []string{cache.Namespace(), db.SyncNamespace, db.Namespace} {
			if err := backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	})
	source, err := cache.SourceSyncStore(0)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestSourceOmnichainStreamHandoff(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			history := handoffFixture(t, dialect)
			slices.Reverse(history.Chains) // Physical groups arrive in the opposite order from tied callbacks.
			var base uint64
			for _, c := range history.Chains {
				if c.Local.ChainID == 1 {
					var err error
					base, err = sourceHistoricalNumber(c.Local.Progress.Finalized["timestamp"])
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			open := func(ctx context.Context, c SourceHistoricalChain) iter.Seq2[SourceRealtimeEvent, error] {
				return func(yield func(SourceRealtimeEvent, error) bool) {
					emit := func(kind string, n, offset uint64) bool {
						b := maps.Clone(c.Local.Progress.Finalized)
						b["number"], b["timestamp"] = Hex(n), Hex(base+offset)
						b["hash"] = fmt.Sprintf("0x%064x", 100*c.Local.ChainID+offset)
						return yield(SourceRealtimeEvent{Type: kind, Block: b}, nil)
					}
					if c.Local.ChainID == 1 {
						if !emit("block", 2, 1) || !emit("block", 3, 4) || !emit("reorg", 2, 1) || !emit("block", 3, 5) {
							return
						}
						emit("finalize", 3, 5)
					} else if emit("block", 4, 3) {
						emit("block", 5, 6)
					}
					<-ctx.Done()
				}
			}
			var deliveries []string
			for update, err := range StreamSourceOmnichain(t.Context(), history, open) {
				if err != nil {
					t.Fatal(err)
				}
				if update.Type == "events" {
					for _, event := range update.Events {
						point, _ := checkpoint.Decode(event["checkpoint"].(string))
						deliveries = append(deliveries, fmt.Sprintf("history %d/%d", point.ChainID, point.BlockNumber))
					}
					continue
				}
				point, err := checkpoint.Decode(update.Checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				line := fmt.Sprintf("%s %d/%d", update.Type, point.ChainID, point.BlockNumber)
				for _, event := range update.Events {
					cp, err := checkpoint.Decode(event["checkpoint"].(string))
					if err != nil {
						t.Fatal(err)
					}
					line += fmt.Sprintf(" %d/%d", cp.ChainID, cp.BlockNumber)
				}
				deliveries = append(deliveries, line)
				if update.Type == "finalize" {
					break
				}
			}
			want := []string{"history 1/0", "history 2/0", "history 1/1", "block 1/2 2/1 1/2", "block 2/4 2/2 2/3 2/4", "block 1/3 1/3", "reorg 1/2", "block 1/3 2/2 2/3 2/4 1/3", "finalize 2/3"}
			if !slices.Equal(deliveries, want) {
				t.Fatalf("deliveries %q, want %q", deliveries, want)
			}
		})
	}
}

func TestSourceOmnichainStreamTermination(t *testing.T) {
	chains := make([]SourceHistoricalChain, 2)
	for i := range chains {
		id := uint64(i + 1)
		chains[i].Local.ChainID = id
		chains[i].Local.Progress = &SourceSyncProgress{ChainID: id, Current: realtimeControlBlock(1), Finalized: realtimeControlBlock(1)}
	}
	for _, tc := range []string{"completed", "early-end", "provider-error", "cancel"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			wantErr := errors.New("provider unavailable")
			var events []protocol.Object
			inputs := slices.Clone(chains)
			if tc == "completed" {
				for i := range inputs {
					p := *inputs[i].Local.Progress
					p.End = p.Current
					inputs[i].Local.Progress = &p
					// Chain 2's event is physically finalized but past the
					// common watermark at the same timestamp on chain 1.
					if p.ChainID == 2 {
						pos := checkpoint.Block(1, 2, 1, true)
						pos.EventType, pos.EventIndex = checkpoint.Blocks, 0
						events = append(events, protocol.Object{"checkpoint": checkpoint.MustEncode(pos), "chain": protocol.Object{"id": 2}})
					}
				}
			}
			waiting := make(chan struct{})
			open := func(run context.Context, c SourceHistoricalChain) iter.Seq2[SourceRealtimeEvent, error] {
				return func(yield func(SourceRealtimeEvent, error) bool) {
					if tc == "completed" {
						yield(SourceRealtimeEvent{}, errors.New("completed chain opened"))
						return
					}
					if c.Local.ChainID == 2 {
						close(waiting)
						<-run.Done()
						return
					}
					<-waiting
					switch tc {
					case "provider-error":
						yield(SourceRealtimeEvent{}, wantErr)
					case "cancel":
						cancel()
						<-run.Done()
					}
				}
			}
			var err error
			var updates []SourceOmnichainUpdate
			streamOmnichainLive(ctx, inputs, events, open, func(update SourceOmnichainUpdate, e error) bool {
				err = e
				if e == nil {
					updates = append(updates, update)
				}
				return true
			})
			switch tc {
			case "completed":
				if err != nil || len(updates) != 1 || updates[0].ChainID != 2 || len(updates[0].Events) != 1 {
					t.Fatalf("updates %+v: %v", updates, err)
				}
			case "early-end":
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("unexpected early end: %v", err)
				}
			case "provider-error":
				if !errors.Is(err, wantErr) {
					t.Fatalf("provider error: %v", err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			}
		})
	}
}

func TestSourceOmnichainStreamPolling(t *testing.T) {
	clients := map[uint64]*rpc.Client{}
	const start = 1700000000
	for _, id := range []uint64{1, 2} {
		client := rpc.New(testanvil.StartChain(t, id, "--no-mining", "--timestamp", fmt.Sprint(start)))
		clients[id] = client
		for n := uint64(1); n <= 4; n++ {
			var result any
			if err := client.Call(t.Context(), "evm_setNextBlockTimestamp", []any{start + n*10}, &result); err != nil {
				t.Fatal(err)
			}
			if err := client.Call(t.Context(), "evm_mine", []any{}, &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			source := handoffStore(t, dialect)
			history := SourceHistoricalCoordinatorOptions{QuerySize: 100}
			for _, id := range []uint64{1, 2} {
				chain := protocol.Object{"id": id, "name": fmt.Sprintf("chain%d", id)}
				blockFilter := protocol.Object{"type": "block", "chainId": id, "sourceId": "Blocks", "interval": float64(1), "offset": float64(0), "fromBlock": float64(1), "toBlock": float64(3), "include": []string{"block.number", "block.timestamp"}}
				callback := protocol.Object{"type": "block", "name": "Blocks:block", "chain": chain, "filter": blockFilter}
				cached, err := GetSourceCachedIntervals(ctx, true, []protocol.Object{blockFilter}, source)
				if err != nil {
					t.Fatal(err)
				}
				head := func(n uint64) protocol.Object {
					b, err := rpc.NewActions(clients[id]).GetSourceBlockByNumber(ctx, Hex(n), false)
					if err != nil {
						t.Fatal(err)
					}
					return b
				}
				progress := &SourceSyncProgress{ChainID: id, Start: head(1), Finalized: head(id), End: head(3)}
				history.Chains = append(history.Chains, SourceHistoricalChain{Chain: chain, CacheRPCRequests: true, ReorgWindow: 120,
					Local: SourceLocalHistoricalOptions{ChainID: id, Requester: clients[id], Progress: progress, Children: NewSourceHistoricalChildren(nil), EventCallbacks: []protocol.Object{callback}, Store: source, CachedIntervals: cached},
				})
			}
			seen := []string{}
			for update, err := range StreamSourceOmnichain(ctx, history, nil) {
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range update.Events {
					cp, err := checkpoint.Decode(event["checkpoint"].(string))
					if err != nil {
						t.Fatal(err)
					}
					seen = append(seen, fmt.Sprintf("%s %d/%d", update.Type, cp.ChainID, cp.BlockNumber))
				}
			}
			want := []string{"events 1/1", "block 2/1", "block 1/2", "block 2/2", "block 1/3", "block 2/3"}
			if !slices.Equal(seen, want) {
				t.Fatalf("ordered callbacks %q, want %q", seen, want)
			}
		})
	}
}

func TestSourceOmnichainStreamPolledFork(t *testing.T) {
	const genesis = 1700000000
	url := testanvil.StartChain(t, 101, "--no-mining", "--timestamp", fmt.Sprint(genesis))
	client := rpc.New(url)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	call := func(method string, params []any, out any) {
		t.Helper()
		if err := client.Call(ctx, method, params, out); err != nil {
			t.Fatal(err)
		}
	}
	mine := func(stamp int) {
		var result any
		call("evm_setNextBlockTimestamp", []any{stamp}, &result)
		call("evm_mine", []any{}, &result)
	}
	mine(genesis + 10)
	base, err := rpc.NewActions(client).GetSourceBlockByNumber(ctx, "latest", false)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot string
	call("evm_snapshot", []any{}, &snapshot)
	mine(genesis + 20)
	local := SourceLocalHistoricalOptions{ChainID: 101, Requester: client, Children: NewSourceHistoricalChildren(nil),
		Progress: &SourceSyncProgress{ChainID: 101, Current: base, Finalized: base},
	}
	chain := SourceHistoricalChain{Chain: protocol.Object{"id": 101, "name": "fork", "pollingInterval": 100}, Local: local, ReorgWindow: 120}
	var hashes []string
	streamOmnichainLive(ctx, []SourceHistoricalChain{chain}, nil, pollOmnichainSource, func(update SourceOmnichainUpdate, err error) bool {
		if err != nil {
			t.Fatal(err)
		}
		switch len(hashes) {
		case 0:
			if update.Type != "block" {
				t.Fatalf("initial update %s", update.Type)
			}
			var reverted bool
			call("evm_revert", []any{snapshot}, &reverted)
			if !reverted {
				t.Fatal("node did not revert")
			}
			mine(genesis + 21)
		case 1:
			if update.Type != "reorg" || update.Source.Block["hash"] != base["hash"] {
				t.Fatalf("wrong ancestor %+v", update)
			}
		case 2:
			if update.Type != "block" || update.Source.Block["number"] != "0x2" || update.Source.Block["hash"] == hashes[0] {
				t.Fatalf("wrong replacement %+v", update)
			}
		}
		hashes = append(hashes, update.Source.Block["hash"].(string))
		return len(hashes) < 3
	})
	if len(hashes) != 3 {
		t.Fatalf("observed hashes %v", hashes)
	}
}
