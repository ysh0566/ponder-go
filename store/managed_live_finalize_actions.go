package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ApplyFinalize commits public finality, safe anchors, prefix cleanup and an
// independent receipt in the business domain. Business rows, public latest,
// private cursors, acquired proofs and ordinal high-water stay untouched.
// Any future sync cleanup must run separately after this receipt, using safe
// anchors and replay retention requirements, not a higher public finalized
// checkpoint. No sync cleanup, distributed transaction or recovery is enabled.
func (m *ManagedLive) ApplyFinalize(ctx context.Context, in ManagedLiveFinalize, acquired *ManagedLiveFinalizeAcquisition) (ManagedLiveFinalizeResult, error) {
	if err := m.usable(ctx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	r, err := normalizeLiveFinalize(m.opts, in)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := m.requireFinalizeAcquisition(r, acquired); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	s := m.owner.s
	if !s.mu.TryLock() {
		return ManagedLiveFinalizeResult{}, ErrManagedLiveBusy
	}
	defer s.mu.Unlock()
	if err := m.usable(ctx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	tx, err := s.Backend.Begin(ctx)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	defer tx.Rollback(context.Background())
	if err := s.lock(ctx, tx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	d, _, err := m.readDurable(ctx, tx)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	for _, op := range d.Operations {
		if op.ID == r.ID {
			evidence, err := exactLiveFinalizeRetry(r, m.finalizeHash(r), s.leaseToken, op)
			if err == nil {
				err = m.owner.requireLease(ctx, tx)
			}
			if err != nil {
				return ManagedLiveFinalizeResult{}, err
			}
			return liveFinalizeResult(evidence, false), nil
		}
	}
	result, err := m.applyFinalizeTransaction(ctx, tx, r, acquired.requestHash)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(context.Background())
		if reconcile := m.reconcileFinalize(ctx, r, acquired, err); reconcile != nil {
			m.uncertain.Store(true)
			return ManagedLiveFinalizeResult{}, reconcile
		}
	}
	return result, nil
}

func (m *ManagedLive) reconcileFinalize(ctx context.Context, r ManagedLiveFinalize, acquired *ManagedLiveFinalizeAcquisition, commitErr error) error {
	unknown := func(err error) error {
		return &ManagedStageError{Stage: "live-finalize", Outcome: "unknown", Err: errors.Join(commitErr, err)}
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
			if _, err := exactLiveFinalizeRetry(r, acquired.requestHash, m.owner.s.leaseToken, op); err != nil {
				return unknown(err)
			}
			if err := m.owner.requireLease(ctx, tx); err != nil {
				return unknown(err)
			}
			return nil
		}
	}
	return unknown(fmt.Errorf("exact finalize receipt not confirmed"))
}

// applyFinalizeTransaction shares the exact durable planner and receipt writer.
// The v11 historical batch calls it before its sole business Commit; legacy
// ApplyFinalize retains its independent transaction and acquisition fence.
func (m *ManagedLive) applyFinalizeTransaction(ctx context.Context, tx Transaction, r ManagedLiveFinalize, requestHash string) (ManagedLiveFinalizeResult, error) {
	s := m.owner.s
	d, beforeHash, err := m.readDurable(ctx, tx)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	baseline, err := liveDurableBaselines(d.Operations)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	plan, err := planLiveFinalize(m.opts, d.Control, baseline, d.Segments, d.Journal, d.Proofs, r, liveExecutedHeads(d.Operations))
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	evidence, err := makeLiveFinalizeEvidence(r, requestHash, d.Control, baseline, plan)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	for _, chain := range m.opts.Chains {
		if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_finalized")+" SET checkpoint=$1,safe_checkpoint=$2 WHERE chain_id=$3", liveFinalCheckpoint(plan.After, baseline, chain.ID), liveSafeCheckpoint(plan.After, baseline, chain.ID), chain.ID); err != nil {
			return ManagedLiveFinalizeResult{}, err
		}
	}
	if len(plan.Pruned) > 0 {
		if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE operation_id<=$1", plan.After.SafeJournalBoundary); err != nil {
			return ManagedLiveFinalizeResult{}, err
		}
		if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_live_segments")+" WHERE ordinal<=$1", plan.After.SafeOrdinal); err != nil {
			return ManagedLiveFinalizeResult{}, err
		}
	}
	controlJSON, err := json.Marshal(plan.After)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_control")+" SET payload=$1::jsonb WHERE key='control'", string(controlJSON)); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_operations")+" (id,generation,owner_token,kind,request_hash,previous_operation,before_hash,after_hash,payload) VALUES($1,$2,$3,'finalize',$4,$5,$6,'',$7::jsonb)", r.ID, r.Generation, s.leaseToken, requestHash, d.Control.LastOperation, beforeHash, string(payload)); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	afterHash, err := m.snapshotHash(ctx, tx, r.ID)
	if err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_operations")+" SET after_hash=$1 WHERE id=$2", afterHash, r.ID); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if _, _, err := m.readDurable(ctx, tx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}
	if err := m.owner.requireLease(ctx, tx); err != nil {
		return ManagedLiveFinalizeResult{}, err
	}

	return liveFinalizeResult(evidence, true), nil
}
