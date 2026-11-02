package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ponder.local/ponder-go/checkpoint"
)

func liveDurableBaselines(ops []managedLiveOperation) (map[uint64]ManagedLiveHead, error) {
	for _, op := range ops {
		if op.Kind != "batch" {
			continue
		}
		var e managedLiveBatchEvidence
		if decodeLiveJSON(op.Payload, &e) != nil {
			return nil, ErrManagedLiveBatchConflict
		}
		if len(e.Batch.Baselines) > 0 {
			return cloneLiveHeads(e.Batch.Baselines), nil
		}
	}
	return nil, ErrManagedLivePendingActions
}

// undoJournal consumes rows in descending actual execution order across ALL
// chains. SQL NULL means an original insert: undo deletes without reinserting.
func liveReorgUndoRows(rows []managedLiveJournalEntry) Result {
	result := Result{Rows: make([]map[string]any, 0, len(rows))}
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		var before any
		if string(row.Before) != "null" {
			before = string(row.Before)
		}
		result.Rows = append(result.Rows, map[string]any{"id": fmt.Sprint(row.ID), "table_name": row.Table, "key": string(row.Key), "before": before})
	}
	return result
}

// ApplyReorg atomically undoes the global business suffix and every affected
// private cursor, archives its journal evidence, and records an exact receipt.
// Public latest/finalized stay untouched. No sync-domain transaction is opened.
// The v3 constructors separately handle nonempty recovery; this is not runtime.
func (m *ManagedLive) ApplyReorg(ctx context.Context, in ManagedLiveReorg, acquired *ManagedLiveReorgAcquisition) (ManagedLiveReorgResult, error) {
	if err := m.usable(ctx); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	r, err := normalizeLiveReorg(m.opts, in)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := m.requireReorgAcquisition(r, acquired); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	s := m.owner.s
	if !s.mu.TryLock() {
		return ManagedLiveReorgResult{}, ErrManagedLiveBusy
	}
	defer s.mu.Unlock()
	if err := m.usable(ctx); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	tx, err := s.Backend.Begin(ctx)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	defer tx.Rollback(context.Background())
	if err := s.lock(ctx, tx); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	d, beforeHash, err := m.readDurable(ctx, tx)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	for _, op := range d.Operations {
		if op.ID == r.ID {
			evidence, err := exactLiveReorgRetry(r, m.reorgHash(r), s.leaseToken, op)
			if err == nil {
				err = m.owner.requireLease(ctx, tx)
			}
			if err != nil {
				return ManagedLiveReorgResult{}, err
			}
			return liveReorgResult(evidence, false), nil
		}
	}
	baselines, err := liveDurableBaselines(d.Operations)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	plan, err := planLiveReorg(m.opts, d.Control, baselines, d.Segments, d.Journal, d.Proofs, r)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	evidence, err := makeLiveReorgEvidence(r, acquired.requestHash, d.Control, plan)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := prepareLiveQueryChanges(ctx, tx, s.Backend.Dialect()); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	ancestor, _ := checkpoint.Decode(r.Ancestor.Checkpoint)
	if err := s.eventContext(ctx, tx, r.Ancestor.Checkpoint, ancestor.ChainID, true); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if _, err := s.undoJournal(ctx, tx, liveReorgUndoRows(plan.Undo)); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE operation_id>$1", plan.JournalBoundary); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_live_segments")+" WHERE ordinal>$1", plan.After.Tail); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	for _, chain := range m.opts.Chains {
		cp, err := checkpoint.Decode(liveCursorCheckpoint(plan.After, chain.ID))
		if err != nil {
			return ManagedLiveReorgResult{}, err
		}
		if err := s.runtimeCursor(ctx, tx, cp); err != nil {
			return ManagedLiveReorgResult{}, err
		}
	}
	if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_live_proofs")+" WHERE chain_id=$1 AND block_number>$2", ancestor.ChainID, ancestor.BlockNumber); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	controlJSON, err := json.Marshal(plan.After)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_control")+" SET payload=$1::jsonb WHERE key='control'", string(controlJSON)); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_operations")+" (id,generation,owner_token,kind,request_hash,previous_operation,before_hash,after_hash,payload) VALUES($1,$2,$3,'reorg',$4,$5,$6,'',$7::jsonb)", r.ID, r.Generation, s.leaseToken, acquired.requestHash, d.Control.LastOperation, beforeHash, string(payload)); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	afterHash, err := m.snapshotHash(ctx, tx, r.ID)
	if err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_operations")+" SET after_hash=$1 WHERE id=$2", afterHash, r.ID); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if _, _, err := m.readDurable(ctx, tx); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveReorgResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(context.Background())
		if reconcile := m.reconcileReorg(ctx, r, acquired, err); reconcile != nil {
			m.uncertain.Store(true)
			return ManagedLiveReorgResult{}, reconcile
		}
	}
	return liveReorgResult(evidence, true), nil
}

func (m *ManagedLive) reconcileReorg(ctx context.Context, r ManagedLiveReorg, acquired *ManagedLiveReorgAcquisition, commitErr error) error {
	unknown := func(err error) error {
		return &ManagedStageError{Stage: "live-reorg", Outcome: "unknown", Err: errors.Join(commitErr, err)}
	}
	if err := ctx.Err(); err != nil {
		return unknown(err)
	}
	tx, err := m.owner.s.Backend.Begin(ctx)
	if err != nil {
		return unknown(err)
	}
	defer tx.Rollback(context.Background())
	if err := m.owner.s.lock(ctx, tx); err != nil {
		return unknown(err)
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return unknown(err)
	}
	d, _, err := m.readDurable(ctx, tx)
	if err != nil {
		return unknown(err)
	}
	for _, op := range d.Operations {
		if op.ID == r.ID {
			if _, err := exactLiveReorgRetry(r, acquired.requestHash, m.owner.s.leaseToken, op); err != nil {
				return unknown(err)
			}
			if err := m.owner.requireLease(ctx, tx); err != nil {
				return unknown(err)
			}
			return nil
		}
	}
	return unknown(fmt.Errorf("exact reorg receipt not confirmed"))
}
