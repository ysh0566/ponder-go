package store

import (
	"context"
)

// ManagedLiveDomainStage observes a completed native transition, outside that
// transaction and inside the fenced coordinator. It grants no SQL capability.
// Observers may be called again on reconciliation; they must be idempotent.
type ManagedLiveDomainStage struct{ Phase, OperationID string }

func (d *ManagedLiveDomains) observeDomain(ctx context.Context, phase, id string) error {
	if d.domainObserver == nil {
		return nil
	}
	if !d.live().opts.liveContinuous {
		return ErrManagedLiveLegacyMutation
	}
	if err := d.domainObserver(ctx, ManagedLiveDomainStage{Phase: phase, OperationID: id}); err != nil {
		return &ManagedStageError{Stage: "runtime-reorg-" + phase, Outcome: "observer", Err: err}
	}
	return nil
}

// RuntimePublicCheckpoints returns authenticated public projections, including
// the stable heads retained while private replay cursors are behind them.
func (d *ManagedLiveDomains) RuntimePublicCheckpoints(ctx context.Context) (map[uint64]string, error) {
	if !d.mu.TryLock() {
		return nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if !d.live().opts.liveRuntime {
		return nil, ErrManagedLiveLegacyMutation
	}
	if err := d.gate(ctx); err != nil {
		return nil, err
	}
	b, _, err := d.business(ctx)
	if err != nil {
		return nil, err
	}
	out := map[uint64]string{}
	for _, state := range b.States {
		out[state.ChainID] = state.Latest
	}
	return out, nil
}

// RuntimeFinalizeHeaders validates the exact prospective business transition
// against native durable receipts and returns a fresh copy of its complete
// executed header lineage. A cache hit is never an execution proof. This read
// grants no SQL capability and does not advance finality or reserve an ID.
func (d *ManagedLiveDomains) RuntimeFinalizeHeaders(ctx context.Context, request ManagedLiveFinalize) (map[uint64]map[uint64]ManagedLiveHead, error) {
	if !d.mu.TryLock() {
		return nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if !d.live().opts.liveContinuous {
		return nil, ErrManagedLiveLegacyMutation
	}
	if d.uncertain {
		return nil, ErrManagedLiveDomainsPending
	}
	ledger, b, _, err := d.audit(ctx)
	if err != nil {
		return nil, err
	}
	if ledger.Pending != "" {
		return nil, ErrManagedLiveDomainsPending
	}
	baselines, err := liveDurableBaselines(b.Operations)
	if err != nil {
		return nil, err
	}
	if _, err = planLiveFinalize(d.live().opts, b.Control, baselines, b.Segments, b.Journal, b.Proofs, request, liveExecutedHeads(b.Operations)); err != nil {
		return nil, err
	}
	proofs, err := liveProofMap(b.Proofs)
	if err != nil {
		return nil, err
	}
	confirmed := livePublicFinality(b.Control, baselines)
	out := map[uint64]map[uint64]ManagedLiveHead{}
	for id, head := range request.Finalized {
		lineage, err := liveHeadLineage(head, confirmed[id], proofs)
		if err != nil {
			return nil, err
		}
		out[id] = lineage
	}
	return out, nil
}
