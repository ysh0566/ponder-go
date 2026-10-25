package runtime

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

type sourceHistoryReadFunc func(context.Context, store.SourceEventDataArgs) (store.SourceEventData, error)

func (f sourceHistoryReadFunc) GetEventData(ctx context.Context, a store.SourceEventDataArgs) (store.SourceEventData, error) {
	return f(ctx, a)
}

func refetchControlCallback(id uint64) protocol.Object {
	return protocol.Object{"type": "block", "name": "Blocks:block", "filter": filter.Object{"type": "block", "chainId": id, "fromBlock": float64(0), "toBlock": filter.Undefined, "interval": float64(1), "offset": float64(0)}}
}
func refetchControlEvent(id, n uint64) protocol.Object {
	return protocol.Object{"chain": protocol.Object{"id": id}, "checkpoint": checkpoint.MustEncode(checkpoint.Block(100+n, id, n, true))}
}
func refetchControlBlock(n uint64) filter.Object {
	return filter.Object{"number": new(big.Int).SetUint64(n), "timestamp": new(big.Int).SetUint64(100 + n)}
}

// These controls exercise production reconstruction/decoding over a narrowly
// recorded read boundary. They are supplemental, not real-database acceptance.
func TestSourceHistoricalRefetchControls(t *testing.T) {
	t.Run("all_filters_page_limit_cursor_and_unfiltered_checkpoint", func(t *testing.T) {
		cb := refetchControlCallback(1)
		cb2 := refetchControlCallback(1)
		calls := []store.SourceEventDataArgs{}
		blocks := []filter.Object{refetchControlBlock(1), refetchControlBlock(2), refetchControlBlock(3)}
		s := sourceHistoryReadFunc(func(_ context.Context, a store.SourceEventDataArgs) (store.SourceEventData, error) {
			calls = append(calls, a)
			if len(calls) == 1 {
				return store.SourceEventData{Blocks: blocks[:2], Cursor: 2}, nil
			}
			return store.SourceEventData{Blocks: blocks[2:], Cursor: 3}, nil
		})
		input := []protocol.Object{refetchControlEvent(1, 1), refetchControlEvent(1, 3)}
		got, err := RefetchLocalSourceEvents(context.Background(), SourceHistoryRefetchOptions{Chain: protocol.Object{"id": uint64(1)}, EventCallbacks: []protocol.Object{cb, cb2}, Children: NewSourceHistoricalChildren(nil), Events: input, Store: s})
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) != 2 || len(got) != 6 {
			t.Fatalf("calls %d events %d", len(calls), len(got))
		}
		for i, a := range calls {
			if a.ChainID != 1 || a.Limit != 2 || a.ToBlock != 3 || a.FromBlock != float64(1+i*2) || len(a.Filters) != 2 {
				t.Fatalf("query %+v", a)
			}
			if reflect.ValueOf(a.Filters[0]).Pointer() != reflect.ValueOf(cb["filter"]).Pointer() {
				t.Fatal("filter identity changed")
			}
		}
		if reflect.ValueOf(got[0].Block).Pointer() != reflect.ValueOf(blocks[0]).Pointer() {
			t.Fatal("raw block identity lost")
		}
		if got[2].Block["number"].(*big.Int).Uint64() != 2 {
			t.Fatal("intermediate block removed by checkpoint filter")
		}
	})
	t.Run("cross_chain_positions_fresh_decode_and_chain_identity", func(t *testing.T) {
		chain1, chain2 := protocol.Object{"id": uint64(1)}, protocol.Object{"id": uint64(2)}
		cb1, cb2 := refetchControlCallback(1), refetchControlCallback(2)
		chains := []SourceHistoricalChain{{Chain: chain1, Local: SourceLocalHistoricalOptions{EventCallbacks: []protocol.Object{cb1}, Children: NewSourceHistoricalChildren(nil)}}, {Chain: chain2, Local: SourceLocalHistoricalOptions{EventCallbacks: []protocol.Object{cb2}, Children: NewSourceHistoricalChildren(nil)}}}
		calls := []uint64{}
		s := sourceHistoryReadFunc(func(_ context.Context, a store.SourceEventDataArgs) (store.SourceEventData, error) {
			calls = append(calls, a.ChainID)
			blocks := []filter.Object{}
			for n := uint64(a.FromBlock); n <= uint64(a.ToBlock); n++ {
				blocks = append(blocks, refetchControlBlock(n))
			}
			return store.SourceEventData{Blocks: blocks, Cursor: a.ToBlock}, nil
		})
		input := []protocol.Object{refetchControlEvent(2, 8), refetchControlEvent(1, 1), refetchControlEvent(2, 9), refetchControlEvent(1, 2)}
		got, err := RefetchHistoricalSourceEvents(context.Background(), chains, input, s, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(calls, []uint64{1, 2}) || len(got) != 4 {
			t.Fatalf("calls %v, got %v", calls, got)
		}
		for i, event := range got {
			id, _ := sourceEventChainID(event)
			want, _ := sourceEventChainID(input[i])
			if id != want {
				t.Fatal("cross-chain position changed")
			}
			if reflect.ValueOf(event).Pointer() == reflect.ValueOf(input[i]).Pointer() {
				t.Fatal("returned stale decoded input")
			}
			chain := chain1
			if id == 2 {
				chain = chain2
			}
			if reflect.ValueOf(event["chain"]).Pointer() != reflect.ValueOf(chain).Pointer() {
				t.Fatal("chain identity lost")
			}
		}
	})
	t.Run("positional_not_checkpoint_match_and_sparse_holes", func(t *testing.T) {
		cb := refetchControlCallback(1)
		chains := []SourceHistoricalChain{{Chain: protocol.Object{"id": uint64(1)}, Local: SourceLocalHistoricalOptions{EventCallbacks: []protocol.Object{cb}, Children: NewSourceHistoricalChildren(nil)}}}
		s := sourceHistoryReadFunc(func(_ context.Context, a store.SourceEventDataArgs) (store.SourceEventData, error) {
			return store.SourceEventData{Blocks: []filter.Object{refetchControlBlock(2)}, Cursor: a.ToBlock}, nil
		})
		input := []protocol.Object{refetchControlEvent(1, 1), refetchControlEvent(9, 1), refetchControlEvent(1, 3)}
		got, err := RefetchHistoricalSourceEvents(context.Background(), chains, input, s, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] == nil || got[1] != nil || got[2] != nil {
			t.Fatalf("source positional holes lost: %v", got)
		}
		cp, err := checkpoint.Decode(got[0]["checkpoint"].(string))
		if err != nil || cp.BlockNumber != 2 {
			t.Fatal("unexpected positional refetch")
		}
	})
	t.Run("read_error_and_cancellation_propagate", func(t *testing.T) {
		sentinel := errors.New("query rejected")
		s := sourceHistoryReadFunc(func(context.Context, store.SourceEventDataArgs) (store.SourceEventData, error) {
			return store.SourceEventData{}, sentinel
		})
		o := SourceHistoryRefetchOptions{Chain: protocol.Object{"id": uint64(1)}, Children: NewSourceHistoricalChildren(nil), EventCallbacks: []protocol.Object{refetchControlCallback(1)}, Events: []protocol.Object{refetchControlEvent(1, 1)}, Store: s}
		if _, err := RefetchLocalSourceEvents(context.Background(), o); err != sentinel {
			t.Fatalf("wrapped read error %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := RefetchLocalSourceEvents(ctx, o); err != context.Canceled {
			t.Fatalf("cancel %v", err)
		}
	})
	t.Run("empty_and_nonadvancing_cursor_reject", func(t *testing.T) {
		s := sourceHistoryReadFunc(func(context.Context, store.SourceEventDataArgs) (store.SourceEventData, error) {
			return store.SourceEventData{Cursor: 0}, nil
		})
		o := SourceHistoryRefetchOptions{Chain: protocol.Object{"id": uint64(1)}, Children: NewSourceHistoricalChildren(nil), Events: []protocol.Object{refetchControlEvent(1, 1)}, Store: s}
		if _, err := RefetchLocalSourceEvents(context.Background(), o); err == nil {
			t.Fatal("nonadvancing cursor accepted")
		}
		o.Events = nil
		if _, err := RefetchLocalSourceEvents(context.Background(), o); err == nil {
			t.Fatal("empty input accepted")
		}
	})
	t.Run("descending_range_decode_rejects_without_query", func(t *testing.T) {
		chain := SourceHistoricalChain{Chain: protocol.Object{"id": uint64(1)}, Local: SourceLocalHistoricalOptions{Children: NewSourceHistoricalChildren(nil), EventCallbacks: []protocol.Object{refetchControlCallback(1)}}}
		s := sourceHistoryReadFunc(func(context.Context, store.SourceEventDataArgs) (store.SourceEventData, error) {
			t.Fatal("descending range must not query")
			return store.SourceEventData{}, nil
		})
		_, err := RefetchHistoricalSourceEvents(context.Background(), []SourceHistoricalChain{chain}, []protocol.Object{refetchControlEvent(1, 3), refetchControlEvent(1, 1)}, s, nil)
		if err == nil {
			t.Fatal("source undefined raw result cannot decode as empty success")
		}
	})
}
