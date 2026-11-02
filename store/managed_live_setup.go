package store

import (
	"context"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
)

type runtimeSetupKey struct{}
type runtimeSetupCallback struct {
	fn func(context.Context, *Writer, func(uint64) error) error
}

// ApplyRuntimeBootstrap executes setup through the same admitted native batch
// transaction as the initial checkpoints and authenticated bootstrap receipt.
func (d *ManagedLiveDomains) ApplyRuntimeBootstrap(ctx context.Context, b ManagedLiveBatch, a *ManagedLiveAcquisition, fn func(context.Context, *Writer, func(uint64) error) error) (ManagedLiveBatchResult, error) {
	if !d.live().opts.runtimeSetup || fn == nil || len(b.Blocks) != 0 || len(b.Baselines) == 0 {
		return ManagedLiveBatchResult{}, ErrManagedLiveBatchConflict
	}
	run := context.WithValue(ctx, runtimeSetupKey{}, &runtimeSetupCallback{fn})
	return d.ApplyRuntimeBatch(run, b, a, nil)
}

func (m *ManagedLive) runRuntimeSetup(ctx context.Context, tx Transaction, baselines map[uint64]ManagedLiveHead, initial map[uint64]string) error {
	cb, ok := ctx.Value(runtimeSetupKey{}).(*runtimeSetupCallback)
	if !ok || cb == nil || cb.fn == nil {
		return fmt.Errorf("v8 bootstrap requires its admitted setup callback: %w", ErrManagedLiveBatchConflict)
	}
	run, admitted := ctx.Value(runtimeCommitContextKey{}).(context.Context)
	if !admitted || run.Value(runtimeBatchKey{}) == nil {
		return ErrManagedLiveBatchConflict
	}
	s := m.owner.s
	first := m.opts.Chains[0].ID
	if err := s.eventContext(ctx, tx, checkpoint.MaxString, first, false); err != nil {
		return err
	}
	w := &Writer{s: s, tx: tx, encoded: checkpoint.MaxString, changed: map[string]bool{}}
	defer func() {
		w.closed.Store(true)
		if orm := w.indexingSession(); orm != nil {
			orm.Cache.mu.Lock()
			clearSessionTables(orm.Cache.tables)
			orm.Cache.mu.Unlock()
		}
	}()
	var previous *checkpoint.Checkpoint
	attribute := func(id uint64) error {
		h, ok := baselines[id]
		if !ok || w.closed.Load() {
			return ErrManagedLiveBatchConflict
		}
		position := h.Checkpoint
		if m.opts.runtimeInitial {
			position = initial[id]
		}
		target, err := checkpoint.Decode(position)
		if err != nil || target.ChainID != id {
			return ErrManagedLiveBatchConflict
		}
		if previous != nil {
			if err = w.flushAndSeal(ctx, *previous); err != nil {
				return err
			}
		} else {
			if orm := w.indexingSession(); orm != nil {
				if err = orm.Cache.Flush(ctx); err != nil {
					return err
				}
			}
			if err = s.requireCommitted(ctx, tx, first); err != nil {
				return err
			}
		}
		if err = s.eventContext(ctx, tx, checkpoint.MaxString, id, false); err != nil {
			return err
		}
		w.event.Checkpoint.ChainID = id
		previous = &target
		return nil
	}
	if err := cb.fn(run, w, attribute); err != nil {
		return err
	}
	if err := run.Err(); err != nil {
		return err
	}
	if previous == nil {
		return fmt.Errorf("setup bootstrap executed no attributed callback")
	}
	if err := w.flushAndSeal(ctx, *previous); err != nil {
		return err
	}
	if w.err != nil {
		return w.err
	}
	// Setup is the committed initial safe prefix, not a replayable live segment.
	// Every setup mutation is first flushed/attributed, then pruned atomically.
	if err := s.pruneInitialSafePrefix(ctx, tx); err != nil {
		return err
	}
	rows, err := m.readLiveJournal(ctx, tx, 0)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return fmt.Errorf("setup journal escaped initial safe prefix")
	}
	return nil
}
