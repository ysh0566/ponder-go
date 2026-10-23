package finality

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func getBlock(n, t uint64) *Block {
	return &Block{Hash: fmt.Sprintf("0x%x", n), ParentHash: fmt.Sprintf("0x%x", max(int64(n)-1, 0)), Number: fmt.Sprintf("0x%x", n), Timestamp: fmt.Sprintf("0x%x", t), LogsBloom: "0x"}
}

type mockRPC struct {
	blocks map[uint64]*Block
	calls  []Request
}

func (r *mockRPC) get(_ context.Context, q Request) (*Block, error) {
	r.calls = append(r.calls, q)
	return r.blocks[q.Number], nil
}
func blocks100() map[uint64]*Block {
	out := map[uint64]*Block{}
	for n := uint64(0); n <= 100; n++ {
		out[n] = getBlock(n, n)
	}
	return out
}

// Exact six source cases; source assertions and call-observation assertions retained.
func TestUpstreamFinality(t *testing.T) {
	ctx := context.Background()
	t.Run("isAsyncExecutionChain()", func(t *testing.T) {
		for _, id := range []uint64{143, 10143, 43114, 43113} {
			if !IsAsyncExecutionChain(id) {
				t.Fatalf("expected true for %d", id)
			}
		}
		if IsAsyncExecutionChain(1) {
			t.Fatal("expected false for 1")
		}
	})
	t.Run("getFinalizedBlock() skips the search when finality has not advanced", func(t *testing.T) {
		lower := getBlock(80, 80)
		rpc := mockRPC{blocks: map[uint64]*Block{}}
		result, e := GetFinalizedBlock(ctx, Chain{ReorgWindow: 30}, rpc.get, getBlock(100, 100), lower)
		if e != nil {
			t.Fatal(e)
		}
		if result != lower {
			t.Fatal("lowerBound identity not retained")
		}
		if len(rpc.calls) != 0 {
			t.Fatal("unexpected RPC request", rpc.calls)
		}
	})
	t.Run("getFinalizedBlock() uses blockTime for its initial guess", func(t *testing.T) {
		rpc := mockRPC{blocks: blocks100()}
		blockTime := 2000.0
		result, e := GetFinalizedBlock(ctx, Chain{ReorgWindow: 20, BlockTimeMS: &blockTime}, rpc.get, getBlock(100, 100), nil)
		if e != nil {
			t.Fatal(e)
		}
		if result.Number != "0x50" {
			t.Fatal(result.Number)
		}
		if len(rpc.calls) == 0 || rpc.calls[0].Number != 90 {
			t.Fatal(rpc.calls)
		}
	})
	t.Run("getFinalizedBlock() assumes one-second blocks without blockTime", func(t *testing.T) {
		rpc := mockRPC{blocks: blocks100()}
		_, e := GetFinalizedBlock(ctx, Chain{ReorgWindow: 20}, rpc.get, getBlock(100, 100), nil)
		if e != nil {
			t.Fatal(e)
		}
		if len(rpc.calls) == 0 || rpc.calls[0].Number != 80 {
			t.Fatal(rpc.calls)
		}
	})
	t.Run("getFinalizedBlock() returns genesis when the chain is younger than the reorg window", func(t *testing.T) {
		genesis := getBlock(0, 100)
		rpc := mockRPC{blocks: map[uint64]*Block{0: genesis, 1: getBlock(1, 110)}}
		result, e := GetFinalizedBlock(ctx, Chain{ReorgWindow: 180}, rpc.get, getBlock(1, 110), nil)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(result, genesis) {
			t.Fatalf("%+v", result)
		}
	})
	t.Run("getFinalizedBlock() returns the newest block at the exact cutoff", func(t *testing.T) {
		rpc := mockRPC{blocks: blocks100()}
		result, e := GetFinalizedBlock(ctx, Chain{ReorgWindow: 20}, rpc.get, getBlock(100, 100), nil)
		if e != nil {
			t.Fatal(e)
		}
		if result.Number != "0x50" {
			t.Fatal(result.Number)
		}
	})
}
