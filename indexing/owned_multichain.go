package indexing

import (
	"context"
	"errors"
	"fmt"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/store"
)

// InitialChainCheckpoint contains sync-progress checkpoints, NOT the block
// selected for setup RPC calls. Initial durable state is lexical min(start,
// finalized), matching the source's fully encoded checkpoint comparison.
type InitialChainCheckpoint struct {
	ChainID   uint64
	Start     string
	Finalized string
}

func (o *Owned) configuredChains() (map[uint64]string, error) {
	out := map[uint64]string{}
	names := map[string]bool{}
	for _, chain := range o.service.options.Build.Chains {
		id, e := clientInteger(chain["id"])
		name, _ := chain["name"].(string)
		if e != nil || !id.IsUint64() || name == "" {
			return nil, fmt.Errorf("invalid configured chain")
		}
		if _, ok := out[id.Uint64()]; ok || names[name] {
			return nil, fmt.Errorf("duplicate configured chain")
		}
		out[id.Uint64()] = name
		names[name] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one configured chain required")
	}
	return out, nil
}

// InitializeMultichain executes ALL setup handlers in one business transaction.
// Validation precedes handlers; journal attribution is sealed per callback, and
// every chain's checkpoint metadata and private cursor commit with the writes.
// This is a finite primitive, not runMultichain or a readiness/cutover API.
func (o *Owned) InitializeMultichain(ctx context.Context, initial []InitialChainCheckpoint) error {
	chains, err := o.configuredChains()
	if err != nil {
		return err
	}
	if len(initial) != len(chains) {
		return fmt.Errorf("initial checkpoints must cover configured chains exactly")
	}
	states := make([]store.CheckpointState, 0, len(initial))
	seen := map[uint64]bool{}
	for _, in := range initial {
		name, ok := chains[in.ChainID]
		if !ok || seen[in.ChainID] {
			return fmt.Errorf("unknown or duplicate initial chain")
		}
		seen[in.ChainID] = true
		for i, encoded := range []string{in.Start, in.Finalized} {
			cp, e := checkpoint.Decode(encoded)
			if e != nil || cp.ChainID != in.ChainID || encoded == checkpoint.MaxString {
				return fmt.Errorf("invalid initial sync checkpoint for chain %d", in.ChainID)
			}
			if cp != checkpoint.Block(cp.BlockTimestamp, cp.ChainID, cp.BlockNumber, i == 1) {
				return fmt.Errorf("initial sync checkpoint requires source start/down and finalized/up tails")
			}

		}
		cp := in.Start
		if in.Finalized < cp {
			cp = in.Finalized
		}
		states = append(states, store.CheckpointState{ChainID: in.ChainID, ChainName: name, SafeCheckpoint: cp, FinalizedCheckpoint: cp, LatestCheckpoint: cp})
	}
	for _, group := range o.service.options.Build.SetupCallbacks {
		for _, cb := range group {
			chain, ok := cb["chain"].(Object)
			if !ok {
				return fmt.Errorf("setup callback lacks chain")
			}
			id, e := clientInteger(chain["id"])
			if e != nil || !id.IsUint64() {
				return fmt.Errorf("invalid setup chain")
			}
			name, ok := chains[id.Uint64()]
			if !ok || chain["name"] != name {
				return fmt.Errorf("setup callback chain not configured")
			}
		}
	}
	return o.boundTransaction(ctx, func(bind func(*store.Writer) (context.Context, func(), error)) error {
		return o.storage.BusinessActions().InitializeMultichain(ctx, states, func(w *store.Writer, attribute func(uint64) error) error {
			run, revoke, e := bind(w)
			if e != nil {
				return e
			}
			defer revoke()
			return o.service.processSetupEvents(run, attribute)
		})
	})
}

// HistoricalGroup processes one already-extracted, already-decoded chain group.
// Committed replay is a no-op; errors never trigger automatic handler retries.
// Callers must resolve CommitOutcomeUnknownError using durable checkpoint
// evidence (or reopen storage), not assume a rollback. Empty groups advance.
func (o *Owned) HistoricalGroup(ctx context.Context, target checkpoint.Checkpoint, events []Object, updateIndexingSeconds func(Object, Object)) (bool, error) {
	chains, err := o.configuredChains()
	if err != nil {
		return false, err
	}
	name, ok := chains[target.ChainID]
	if !ok {
		return false, fmt.Errorf("historical group chain not configured")
	}
	encoded, err := checkpoint.Encode(target)
	if err != nil || encoded == checkpoint.MaxString {
		return false, fmt.Errorf("invalid historical group checkpoint")
	}
	for _, event := range events {
		chain, ok := event["chain"].(Object)
		if !ok {
			return false, fmt.Errorf("historical event lacks chain")
		}
		id, e := clientInteger(chain["id"])
		if e != nil || !id.IsUint64() || id.Uint64() != target.ChainID || chain["name"] != name {
			return false, fmt.Errorf("historical event chain mismatch")
		}
		cp, ok := event["checkpoint"].(string)
		if !ok {
			return false, fmt.Errorf("historical event lacks checkpoint")
		}
		decoded, e := checkpoint.Decode(cp)
		if e != nil || decoded.ChainID != target.ChainID || cp > encoded {
			return false, fmt.Errorf("historical event checkpoint outside group")
		}
	}
	applied := false
	// Only completed-event metrics are transaction-like in the source. Learned
	// profiles and attempt/event counters deliberately survive handler rollback.
	var completed map[string]uint64
	err = o.boundTransaction(ctx, func(bind func(*store.Writer) (context.Context, func(), error)) error {
		states, e := o.storage.BusinessActions().Checkpoints(ctx)
		if e != nil {
			return e
		}
		if len(states) != len(chains) {
			return fmt.Errorf("durable chain configuration mismatch")
		}
		for _, state := range states {
			if name, ok := chains[state.ChainID]; !ok || name != state.ChainName {
				return fmt.Errorf("durable chain configuration mismatch")
			}
		}
		cursor, e := o.storage.BusinessActions().RuntimeCheckpoint(ctx, target.ChainID)
		if e != nil {
			return e
		}
		if encoded > cursor {
			previous := cursor
			for _, event := range events {
				cp := event["checkpoint"].(string)
				if cp <= cursor || cp < previous {
					return fmt.Errorf("historical events overlap durable cursor or are unordered")
				}
				previous = cp
			}
		}
		applied, e = o.storage.BusinessActions().ProcessHistoricalGroup(ctx, name, target, func(w *store.Writer) error {
			run, revoke, e := bind(w)
			if e != nil {
				return e
			}
			defer revoke()
			completed = map[string]uint64{}
			for k, v := range o.service.options.Metrics.Completed {
				completed[k] = v
			}
			return o.service.ProcessHistoricalEvents(run, events, updateIndexingSeconds)
		})
		var uncertain *store.CommitOutcomeUnknownError
		if e != nil && !errors.As(e, &uncertain) && completed != nil {
			for k := range o.service.options.Metrics.Completed {
				delete(o.service.options.Metrics.Completed, k)
			}
			for k, v := range completed {
				o.service.options.Metrics.Completed[k] = v
			}
		}
		return e
	})
	return applied, err
}

// boundTransaction calls Finish only after the OUTER transaction settles. A
// single binding covers every setup callback; normal revocation does not cancel
// pending independent cache/RPC jobs. Detached callback goroutines are forbidden.
func (o *Owned) boundTransaction(ctx context.Context, run func(func(*store.Writer) (context.Context, func(), error)) error) (err error) {
	release, err := o.enterWork()
	if err != nil {
		return err
	}
	var binding *DatabaseBinding
	defer func() {
		if p := recover(); p != nil {
			if binding != nil {
				binding.Revoke()
				_ = binding.Finish(fmt.Errorf("transaction panicked"))
			}
			release()
			panic(p)
		}
		if binding != nil {
			err = errors.Join(err, binding.Finish(err))
		}
		release()
	}()
	return run(func(w *store.Writer) (context.Context, func(), error) {
		bound, b, e := o.database.Bind(ctx, w)
		if e != nil {
			return nil, nil, e
		}
		binding = b
		return bound, b.Revoke, nil
	})
}

// Checkpoints reads durable business progress under the owner's lifecycle gate.
// It is the evidence boundary after an ambiguous commit; it does not retry work.
func (o *Owned) Checkpoints(ctx context.Context) ([]store.CheckpointState, error) {
	release, err := o.enterWork()
	if err != nil {
		return nil, err
	}
	defer release()
	return o.storage.BusinessActions().Checkpoints(ctx)
}

// RevertBusiness preserves the existing global-suffix/safe-prefix rules and
// invalidates reusable row predictions even on an uncertain failure. It does
// NOT atomically rewind the independent sync domain or reset runner cursors.
func (o *Owned) RevertBusiness(ctx context.Context, target checkpoint.Checkpoint) error {
	release, err := o.enterWork()
	if err != nil {
		return err
	}
	defer release()
	if err = o.database.session.Invalidate(); err != nil {
		return err
	}
	return o.storage.BusinessActions().RevertBusiness(ctx, target)
}
