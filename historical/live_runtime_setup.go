package historical

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/rpc"
	source "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func freezeRuntimeSetup(b *config.ExecutableSourceIndexingBuild) error {
	groups := make([][]indexing.Object, len(b.SetupCallbacks))
	for i, group := range b.SetupCallbacks {
		groups[i] = make([]indexing.Object, len(group))
		for j, cb := range group {
			if err := validateHandler(cb); err != nil {
				return err
			}
			chain, ok := cb["chain"].(map[string]any)
			id, ie := uintQuantity(chain["id"])
			want, we := uintQuantity(b.Chains[i]["id"])
			name, nok := chain["name"].(string)
			expected, eok := b.Chains[i]["name"].(string)
			if !ok || ie != nil || we != nil || id != want || !nok || !eok || name == "" || name != expected {
				return fmt.Errorf("runtime setup chain mismatch")
			}
			n := uint64(0)
			v := cb["block"]
			if v != nil && !filter.IsUndefined(v) {
				var err error
				n, err = uintQuantity(v)
				if err != nil || n > 9007199254740991 {
					return fmt.Errorf("invalid runtime setup RPC block")
				}
			}
			copy := indexing.Object{}
			for k, v := range cb {
				copy[k] = v
			}
			copy["block"] = n
			copy["chain"] = b.Chains[i]
			groups[i][j] = copy
		}
	}
	b.SetupCallbacks = groups
	return nil
}

// The managed plan validates metadata/cycles before this copier is used.
func frozenRuntimeValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	if v.CanInterface() {
		if filter.IsUndefined(v.Interface()) {
			return v
		}
		if n, ok := v.Interface().(*big.Int); ok {
			if n == nil {
				return v
			}
			return reflect.ValueOf(new(big.Int).Set(n))
		}
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		o := reflect.New(v.Type()).Elem()
		o.Set(frozenRuntimeValue(v.Elem()))
		return o
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		o := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			o.SetMapIndex(it.Key(), frozenRuntimeValue(it.Value()))
		}
		return o
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		o := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			o.Index(i).Set(frozenRuntimeValue(v.Index(i)))
		}
		return o
	case reflect.Array:
		o := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			o.Index(i).Set(frozenRuntimeValue(v.Index(i)))
		}
		return o
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		o := reflect.New(v.Type().Elem())
		o.Elem().Set(frozenRuntimeValue(v.Elem()))
		return o
	}
	return v
}

func (r *RuntimeRunner) lastSetupBlock(i int) uint64 {
	var height uint64
	for _, cb := range r.build.SetupCallbacks[i] {
		height = max(height, cb["block"].(uint64))
	}
	return height
}

// acquireBootstrap authenticates the initial position before any setup writes.
func (r *RuntimeRunner) acquireBootstrap(epoch *indexing.RuntimeEpoch, i int, id uint64) (store.ManagedLiveHead, string, error) {
	final, err := checkpoint.Decode(r.finalized[id].Checkpoint)
	if err != nil {
		return store.ManagedLiveHead{}, "", err
	}
	var n uint64
	if r.options.SourceCheckpoints {
		n = min(r.starts[id], final.BlockNumber)
	} else {
		n = min(r.starts[id]-1, final.BlockNumber, r.ends[id])
	}
	raw, err := rpc.NewActions(epoch.Requester(i)).GetSourceBlockByNumber(epoch.Context(), source.Hex(n), true)
	if err != nil {
		return store.ManagedLiveHead{}, "", err
	}
	head, err := runtimeHead(id, raw)
	if err != nil {
		return head, "", err
	}
	if r.setup || r.options.SourceCheckpoints {
		if err := r.walkFinalizedLineage(epoch.Context(), epoch.Requester(i), id, head, r.setupLineageCheck(i, id)); err != nil {
			return head, "", err
		}
	}
	position := head.Checkpoint
	if r.options.SourceCheckpoints && r.starts[id] <= final.BlockNumber {
		cp, err := checkpoint.Decode(position)
		if err != nil {
			return head, "", err
		}
		position = checkpoint.MustEncode(checkpoint.Block(cp.BlockTimestamp, id, cp.BlockNumber, false))
	}
	return head, position, nil
}

func (r *RuntimeRunner) bootstrapRuntime(ctx context.Context, epoch *indexing.RuntimeEpoch, c store.ManagedLiveControl) (store.ManagedLiveControl, error) {
	isolated := r.setup && r.build.Ordering == string(schema.Isolated)
	if c.Batches != 0 && (!isolated || len(c.Cursors) == len(r.build.Chains)) {
		return c, nil
	}
	apply := func(heads map[uint64]store.ManagedLiveHead, positions map[uint64]string) error {
		b := store.ManagedLiveBatch{ID: r.nextID("bootstrap"), Generation: c.Generation, ExpectedLastOperation: c.LastOperation, ExpectedTail: c.Tail, Baselines: heads}
		if r.options.SourceCheckpoints {
			b.InitialCheckpoints = positions
		}
		a, err := r.domains.RetainBatch(ctx, b)
		if err != nil {
			return err
		}
		return r.retryHistorical(epoch.Context(), func() error {
			var err error
			if r.setup {
				_, err = epoch.Bootstrap(epoch.Context(), b, a)
			} else {
				_, err = r.domains.ApplyRuntimeBatch(epoch.Context(), b, a, nil)
			}
			return err
		})
	}
	heads := map[uint64]store.ManagedLiveHead{}
	positions := map[uint64]string{}
	var failures []error
	for i, chain := range r.build.Chains {
		id := runtimeChainID(chain)
		if _, done := c.Cursors[id]; done {
			continue
		}
		if err := ctx.Err(); err != nil {
			return c, err
		}
		head, pos, err := r.acquireBootstrap(epoch, i, id)
		if !isolated {
			if err != nil {
				return c, err
			}
			heads[id], positions[id] = head, pos
			continue
		}
		if err == nil {
			err = apply(map[uint64]store.ManagedLiveHead{id: head}, map[uint64]string{id: pos})
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("setup chain %d: %w", id, err))
		}
		// Read the actual committed predecessor before trying the next chain.
		c, err = r.domains.Control(ctx)
		if err != nil {
			return c, errors.Join(append(failures, err)...)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return c, err
	}
	if !isolated {
		if err := apply(heads, positions); err != nil {
			return c, err
		}
		var err error
		c, err = r.domains.Control(ctx)
		if err != nil {
			return c, err
		}
	}
	if r.setup && r.options.OnSetupCommitted != nil {
		if err := r.options.OnSetupCommitted(context.WithValue(ctx, runtimeRunnerCallbackKey{}, r)); err != nil {
			return c, err
		}
	}
	return c, nil
}
