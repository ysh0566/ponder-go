package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/store"
)

type StagedOptions struct {
	Options
	BuildID string
}

func (e *Engine) eventClient(w *store.Writer) *rpc.Client {
	if e.cache == nil {
		return e.Client
	}
	return e.Client.WithCache(w.EventRPCCache())
}

type stagedState struct {
	db                    *store.Lifecycle
	buildID               string
	migrated, initialized bool
	recovery              []store.RecoveryCheckpoint
	historyEnd            uint64
	realtime              atomic.Bool
}

// NewStaged uses the source-shaped Lifecycle/Actions path. Sync performs lease
// acquisition/recovery, setup, historical sealing, then the realtime handoff.
// The caller closes Lifecycle and Backend. Scheduling is currently single-chain.
func NewStaged(c config.Config, db *store.Lifecycle, handlers map[string]Handler, options StagedOptions) (*Engine, error) {
	if db == nil || options.BuildID == "" {
		return nil, fmt.Errorf("staged runtime requires lifecycle and build ID")
	}
	e, err := NewWithOptions(c, db.RuntimeStore(), handlers, options.Options)
	if err != nil {
		return nil, err
	}
	e.staged = &stagedState{db: db, buildID: options.BuildID}
	return e, nil
}
func (e *Engine) Phase() string {
	if e.staged == nil {
		return "atomic"
	}
	if e.staged.realtime.Load() {
		return "realtime"
	}
	return "historical"
}
func (e *Engine) initializeStaged(ctx context.Context, head Block) error {
	s := e.staged
	if s.initialized {
		return nil
	}
	start := ^uint64(0)
	for _, c := range e.Config.Contracts {
		start = min(start, c.StartBlock)
		if f, ok := e.factories[c.Name]; ok && f.FromBlock != nil {
			start = min(start, *f.FromBlock)
		}
	}
	for _, account := range e.Config.Accounts {
		start = min(start, account.StartBlock)
		if f, ok := e.factories[account.Name]; ok && f.FromBlock != nil {
			start = min(start, *f.FromBlock)
		}
	}
	for _, b := range e.Config.Blocks {
		start = min(start, b.StartBlock)
	}
	if start == ^uint64(0) {
		return fmt.Errorf("no indexing sources configured")
	}
	if !s.migrated {
		final, err := e.FinalizedBlock(ctx, head)
		if err != nil {
			return err
		}
		n, err := Number(final.Number)
		if err != nil {
			return err
		}
		s.historyEnd = n
		ts, err := Number(final.Timestamp)
		if err != nil {
			return err
		}
		s.recovery, err = s.db.Migrate(ctx, store.Migration{BuildID: s.buildID, Chains: []store.MigrationChain{{ID: e.chain.ID, Name: e.chain.Name}}, FinalizedBlocks: []store.FinalizedBlock{{Number: n, Timestamp: ts, Hash: final.Hash, ParentHash: final.ParentHash}}})
		if err != nil {
			return err
		}
		s.migrated = true
	}
	if len(s.recovery) > 0 {
		if len(s.recovery) != 1 || s.recovery[0].ChainID != e.chain.ID {
			return fmt.Errorf("staged recovery chain mismatch")
		}
		safe, err := checkpoint.Decode(s.recovery[0].Checkpoint)
		if err != nil {
			return err
		}
		if err = s.db.Actions().ResumeRuntime(ctx, safe); err != nil {
			return err
		}
	} else {
		n := min(start, s.historyEnd)
		b, err := e.block(ctx, Hex(n))
		if err != nil {
			return err
		}
		ts, err := Number(b.Timestamp)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if err = e.Store.SaveBlock(ctx, store.SyncBlock{ChainID: e.chain.ID, Number: n, Hash: b.Hash, ParentHash: b.ParentHash, Timestamp: ts, Payload: payload}); err != nil {
			return err
		}
		initial := checkpoint.Block(ts, e.chain.ID, n, start > n)
		cp := checkpoint.MustEncode(initial)
		state := store.CheckpointState{ChainID: e.chain.ID, ChainName: e.chain.Name, LatestCheckpoint: cp, FinalizedCheckpoint: cp, SafeCheckpoint: cp}
		if err = s.db.Actions().InitializeRuntime(ctx, state, func(w *store.Writer) error {
			for _, contract := range e.Config.Contracts {
				name := contract.Name + ":setup"
				handler := e.Handlers[name]
				if handler == nil {
					continue
				}
				event := Event{Name: name, SetupBlock: contract.StartBlock, Block: Block{Number: Hex(contract.StartBlock)}}
				if err := handler(ctx, &Context{Event: event, DB: w, Client: e.Client, Chain: e.chain}); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	s.initialized = true
	return nil
}
func (e *Engine) startRealtime(ctx context.Context) error {
	s := e.staged
	if s.realtime.Load() {
		return nil
	}
	a := s.db.Actions()
	if err := a.CreateIndexes(ctx); err != nil {
		return err
	}
	if err := a.CreateTriggers(ctx); err != nil {
		return err
	}
	if err := a.CreateLiveQueryProcedures(ctx); err != nil {
		return err
	}
	tables := make([]string, len(e.Store.Schema.Tables))
	for i, t := range e.Store.Schema.Tables {
		tables[i] = t.Name
	}
	if err := a.CreateLiveQueryTriggers(ctx, tables); err != nil {
		return err
	}
	if err := a.CreateViews(ctx); err != nil {
		return err
	}
	if err := s.db.MarkReady(ctx); err != nil {
		return err
	}
	s.realtime.Store(true)
	return nil
}
func (e *Engine) finalizeStaged(ctx context.Context, head Block) error {
	b, err := e.FinalizedBlock(ctx, head)
	if err != nil {
		return err
	}
	n, err := Number(b.Number)
	if err != nil {
		return err
	}
	ts, err := Number(b.Timestamp)
	if err != nil {
		return err
	}
	states, err := e.staged.db.Actions().Checkpoints(ctx)
	if err != nil {
		return err
	}
	if len(states) != 1 {
		return fmt.Errorf("missing single staged chain state")
	}
	target := checkpoint.Block(ts, e.chain.ID, n, true)
	cp := checkpoint.MustEncode(target)
	if cp <= states[0].FinalizedCheckpoint || cp > states[0].LatestCheckpoint {
		return nil
	}
	return e.staged.db.Actions().Finalize(ctx, target)
}
