package store

import (
	"context"
	"fmt"
	"github.com/ysh0566/ponder-go/checkpoint"
)

func (o ManagedLiveOptions) isolatedSetup() bool { return o.runtimeIsolated && o.runtimeSetup }

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
	if m.opts.isolatedSetup() {
		for id := range baselines {
			first = id
		}
	}
	if err := s.eventContext(ctx, tx, checkpoint.MaxString, first, false); err != nil {
		return err
	}
	w := s.newWriter(ctx, tx, Event{}, checkpoint.MaxString)
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
		if !m.opts.isolatedSetup() {
			return fmt.Errorf("setup bootstrap executed no attributed callback")
		}
		// This chain has event sources but no setup handlers.
		if err := attribute(first); err != nil {
			return err
		}
	}
	if err := w.flushAndSeal(ctx, *previous); err != nil {
		return err
	}
	if w.err != nil {
		return w.err
	}
	// Setup is the committed initial safe prefix, not a replayable live segment.
	// Every setup mutation is first flushed/attributed, then pruned atomically.
	if m.opts.isolatedContinuous() {
		// Other chains may have live segments whose undo rows must survive setup.
		if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE chain_id=$1 AND checkpoint<=$2", first, initial[first]); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT 1 FROM "+s.internal("_ponder_go_journal")+" WHERE chain_id=$1 LIMIT 1", first)
		if err != nil {
			return err
		}
		if len(rows.Rows) != 0 {
			return fmt.Errorf("isolated setup journal escaped its initial checkpoint")
		}
		return nil
	}
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
