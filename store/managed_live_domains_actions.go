package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ponder.local/ponder-go/checkpoint"
)

// domainProof authenticates both outcomes against the fully replayed business
// receipt chain. A restart may undo MORE than the requested suffix, but only an
// actual recovery receipt with unchanged finality/safe prefix can cover it.
func (d *ManagedLiveDomains) domainProof(intent liveDomainIntent, b managedLiveDurable) (ManagedLiveDomainResult, bool, error) {
	out := ManagedLiveDomainResult{OperationID: intent.Request.Reorg.ID}
	ordered, err := orderLiveOperations(b.Operations, b.Control.LastOperation)
	if err != nil {
		return out, false, err
	}
	start := -1
	for i, op := range ordered {
		if op.ID == intent.Before.LastOperation {
			if op.AfterHash != intent.BeforeHash {
				return out, false, ErrManagedLiveReceiptConflict
			}
			if err := d.validateIntentAnchor(intent, op); err != nil {
				return out, false, err
			}
			start = i
			break
		}
	}
	if start < 0 {
		return out, false, ErrManagedLiveReceiptConflict
	}
	originalProofs, err := liveDomainProofPrefix(ordered[:start+1])
	if err != nil {
		return out, false, err
	}
	if err = validateDomainOrphans(intent.Request, intent.Before, originalProofs); err != nil {
		return out, false, err
	}
	if start == len(ordered)-1 {
		if err = d.validateIntentRequest(intent.Request, b); err != nil {
			return out, false, err
		}
	}

	for _, op := range ordered[start+1:] {
		switch op.Kind {
		case "reorg":
			if out.BusinessOperation != "" || op.ID != intent.Request.Reorg.ID || op.OwnerToken != intent.Owner || op.Generation != intent.Request.Reorg.Generation || op.RequestHash != intent.RequestHash {
				return out, false, ErrManagedLiveReceiptConflict
			}
			e, err := exactLiveReorgRetry(intent.Request.Reorg, intent.RequestHash, intent.Owner, op)
			if err != nil {
				return out, false, err
			}
			result := liveReorgResult(e, true)
			out.Outcome = "reorg"
			out.BusinessOperation = op.ID
			out.BusinessHash = op.AfterHash
			out.Reorg = &result
		case "recover":
			var e managedLiveRecoveryEvidence
			if decodeLiveJSON(op.Payload, &e) != nil || e.BeforeControl.SafeOrdinal != intent.Before.SafeOrdinal || e.AfterControl.SafeOrdinal != intent.Before.SafeOrdinal || e.AfterControl.Next != intent.Before.Next || managedHash(e.BeforeControl.Finality) != managedHash(intent.Before.Finality) || managedHash(e.AfterControl.Finality) != managedHash(intent.Before.Finality) || managedHash(e.AfterControl.SafeHeads) != managedHash(intent.Before.SafeHeads) {
				return out, false, ErrManagedLiveReceiptConflict
			}
			if out.BusinessOperation == "" {
				if managedHash(e.BeforeControl) != managedHash(intent.Before) || e.BeforeOwner != intent.Owner {
					return out, false, ErrManagedLiveReceiptConflict
				}
				original := b
				original.Control = intent.Before
				original.Segments = e.Removed
				original.Proofs = originalProofs
				original.Journal, err = decodeLiveRecoveryUndo(e)
				if err != nil {
					return out, false, err
				}
				if err = d.validateIntentRequest(intent.Request, original); err != nil {
					return out, false, err
				}
			}
			ancestor, _ := checkpoint.Decode(intent.Request.Reorg.Ancestor.Checkpoint)
			cursor, e2 := checkpoint.Decode(e.AfterControl.Cursors[ancestor.ChainID].Checkpoint)
			if e2 != nil || cursor.BlockNumber > ancestor.BlockNumber {
				return out, false, ErrManagedLiveReceiptConflict
			}
			out.Outcome = "covered-by-recovery"
			out.BusinessOperation = op.ID
			out.BusinessHash = op.AfterHash
			out.Reorg = nil
		default:
			return out, false, ErrManagedLiveReceiptConflict
		}
	}
	return out, out.BusinessOperation != "", nil
}

// Completed receipts remain valid after later normal work. Verify only the
// bounded business prefix ending at the named completion, never return a stale
// reorg plan as a recovery plan.
func (d *ManagedLiveDomains) checkAck(intent liveDomainIntent, ack liveDomainAck, b managedLiveDurable) error {
	ordered, err := orderLiveOperations(b.Operations, b.Control.LastOperation)
	if err != nil {
		return err
	}
	for i, op := range ordered {
		if op.ID == ack.Result.BusinessOperation {
			if op.AfterHash != ack.Result.BusinessHash {
				return ErrManagedLiveReceiptConflict
			}
			prefix := b
			prefix.Operations = ordered[:i+1]
			prefix.Control.LastOperation = op.ID
			result, ok, e := d.domainProof(intent, prefix)
			if e != nil || !ok || managedHash(result) != managedHash(ack.Result) {
				return errors.Join(ErrManagedLiveReceiptConflict, e)
			}
			return nil
		}
	}
	return ErrManagedLiveReceiptConflict
}

func (d *ManagedLiveDomains) audit(ctx context.Context) (liveDomainLedger, managedLiveDurable, string, error) {
	l, err := d.readLedger(ctx, d.storage.syncBackend)
	if err != nil {
		return l, managedLiveDurable{}, "", err
	}
	b, h, err := d.business(ctx)
	if err != nil {
		return l, b, h, err
	}
	if err = d.checkBinding(l, b); err != nil {
		return l, b, h, err
	}
	for id, ack := range l.Acks {
		if err = d.checkAck(l.Intents[id], ack, b); err != nil {
			return l, b, h, err
		}
	}
	if err = d.checkRuntimeLedger(l, b); err != nil {
		return l, b, h, err
	}
	return l, b, h, nil
}

func (d *ManagedLiveDomains) validateIntentRequest(in ManagedLiveDomainReorg, b managedLiveDurable) error {
	baselines, err := liveDurableBaselines(b.Operations)
	if err != nil {
		return err
	}
	if _, err = planLiveReorg(d.live().opts, b.Control, baselines, b.Segments, b.Journal, b.Proofs, in.Reorg); err != nil {
		return err
	}
	return validateDomainOrphans(in, b.Control, b.Proofs)
}

func (d *ManagedLiveDomains) prepareIntent(ctx context.Context, in ManagedLiveDomainReorg, l liveDomainLedger, b managedLiveDurable, beforeHash string) (liveDomainIntent, error) {
	intent := liveDomainIntent{Request: in, Spec: d.spec, Identity: d.live().owner.identity, Owner: d.live().owner.s.leaseToken, RequestHash: d.live().reorgHash(in.Reorg), BeforeHash: beforeHash, Before: b.Control, Deleted: []string{}}
	cache := d.storage.syncCache
	err := cache.transaction(ctx, func(tx Transaction) error {
		current, e := d.readLedger(ctx, tx)
		if e != nil {
			return e
		}
		if current.Head != l.Head || current.Pending != "" {
			return ErrManagedLiveDomainsPending
		}
		if len(in.Orphaned) > 0 && d.runtimePrunesAt(in.Reorg.Ancestor) {
			_, ancestor, _ := normalizeLiveHead(in.Reorg.Ancestor)
			args := []any{ancestor.ChainID}
			slots := []string{}
			numbers := []uint64{}
			for _, h := range in.Orphaned {
				cp, _ := checkpoint.Decode(h.Checkpoint)
				args = append(args, cp.BlockNumber)
				numbers = append(numbers, cp.BlockNumber)
				slots = append(slots, fmt.Sprintf("$%d", len(args)))
			}
			r, e := tx.Query(ctx, "SELECT row_to_json(r)::text AS row FROM "+cache.table("rpc_request_results")+" r WHERE chain_id=$1 AND block_number IN ("+strings.Join(slots, ",")+") ORDER BY request_hash", args...)
			if e != nil {
				return e
			}
			for _, row := range r.Rows {
				intent.Deleted = append(intent.Deleted, fmt.Sprint(row["row"]))
			}
			if e = cache.pruneSourceRPC(ctx, tx, ancestor.ChainID, numbers); e != nil {
				return e
			}
		}
		return d.appendLedger(ctx, tx, current, "prepare", in.Reorg.ID, intent)
	})
	if err == nil {
		return intent, nil
	}
	// A failed ACK is not rollback evidence. The immutable intent and prune share
	// a native sync transaction, so an exact authenticated intent proves commit.
	actual, e := d.readLedger(ctx, d.storage.syncBackend)
	if e == nil {
		if got, ok := actual.Intents[in.Reorg.ID]; ok && managedHash(got) == managedHash(intent) {
			return got, nil
		}
		if actual.Head == l.Head {
			return intent, err
		}
	}
	d.uncertain = true
	return intent, &ManagedStageError{Stage: "domains-sync-prepare", Outcome: "unknown", Err: errors.Join(err, e)}
}

func (d *ManagedLiveDomains) acknowledge(ctx context.Context, l liveDomainLedger, intent liveDomainIntent, result ManagedLiveDomainResult) error {
	ack := liveDomainAck{IntentHash: managedHash(intent), Result: result}
	err := d.storage.syncCache.transaction(ctx, func(tx Transaction) error {
		current, e := d.readLedger(ctx, tx)
		if e != nil {
			return e
		}
		if current.Head != l.Head || current.Pending != result.OperationID {
			return ErrManagedLiveDomainsPending
		}
		return d.appendLedger(ctx, tx, current, "ack", result.OperationID, ack)
	})
	if err == nil {
		return nil
	}
	actual, e := d.readLedger(ctx, d.storage.syncBackend)
	if e == nil {
		if got, ok := actual.Acks[result.OperationID]; ok && managedHash(got) == managedHash(ack) {
			return nil
		}
		if actual.Head == l.Head {
			return err
		}
	}
	d.uncertain = true
	return &ManagedStageError{Stage: "domains-sync-ack", Outcome: "unknown", Err: errors.Join(err, e)}
}

func (d *ManagedLiveDomains) finishPending(ctx context.Context, l liveDomainLedger, b managedLiveDurable) (ManagedLiveDomainResult, error) {
	intent, ok := l.Intents[l.Pending]
	if !ok {
		return ManagedLiveDomainResult{}, ErrManagedLiveReceiptConflict
	}
	result, committed, err := d.domainProof(intent, b)
	if err != nil {
		return result, err
	}
	if !committed {
		// Same-generation exact retry only. A new owner must have a real recovery
		// receipt; it may never impersonate the old acquisition or discard pending.
		if managedHash(b.Control) != managedHash(intent.Before) || d.live().owner.s.leaseToken != intent.Owner || d.live().reorgHash(intent.Request.Reorg) != intent.RequestHash {
			return result, ErrManagedLiveReceiptConflict
		}
		acquired, e := d.live().RetainReorg(ctx, intent.Request.Reorg)
		if e != nil {
			return result, e
		}
		if _, e = d.live().ApplyReorg(ctx, intent.Request.Reorg, acquired); e != nil {
			return result, e
		}
		b, _, err = d.business(ctx)
		if err != nil {
			return result, err
		}
		result, committed, err = d.domainProof(intent, b)
		if err != nil || !committed {
			return result, errors.Join(ErrManagedLiveDomainsPending, err)
		}
	}
	if err = d.observeDomain(ctx, "business", result.OperationID); err != nil {
		return result, err
	}
	if err = d.acknowledge(ctx, l, intent, result); err != nil {
		return result, err
	}
	if err = d.observeDomain(ctx, "ack", result.OperationID); err != nil {
		return result, err
	}
	return result, nil
}

func (d *ManagedLiveDomains) CoordinateReorg(ctx context.Context, in ManagedLiveDomainReorg) (ManagedLiveDomainResult, error) {
	if !d.mu.TryLock() {
		return ManagedLiveDomainResult{}, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if d.uncertain {
		return ManagedLiveDomainResult{}, ErrManagedLiveDomainsPending
	}
	request, err := normalizeDomainReorg(d.live().opts, in)
	if err != nil {
		return ManagedLiveDomainResult{}, err
	}
	l, b, h, err := d.audit(ctx)
	if err != nil {
		return ManagedLiveDomainResult{}, err
	}
	if intent, ok := l.Intents[request.Reorg.ID]; ok {
		if managedHash(intent.Request) != managedHash(request) {
			return ManagedLiveDomainResult{}, ErrManagedLiveReceiptConflict
		}
		if ack, ok := l.Acks[request.Reorg.ID]; ok {
			return ack.Result, nil
		}
		if l.Pending != request.Reorg.ID {
			return ManagedLiveDomainResult{}, ErrManagedLiveDomainsPending
		}
		return d.finishPending(ctx, l, b)
	}
	if d.live().opts.liveRuntime {
		if b.Control.Serving {
			return ManagedLiveDomainResult{}, ErrManagedLivePendingActions
		}
		if err := d.runtimeQuiescent(); err != nil {
			return ManagedLiveDomainResult{}, err
		}
	}
	if l.Pending != "" {
		return ManagedLiveDomainResult{}, ErrManagedLiveDomainsPending
	}
	if err = d.validateIntentRequest(request, b); err != nil {
		return ManagedLiveDomainResult{}, err
	}
	if _, err = d.prepareIntent(ctx, request, l, b, h); err != nil {
		return ManagedLiveDomainResult{}, err
	}
	if err = d.observeDomain(ctx, "prepare", request.Reorg.ID); err != nil {
		return ManagedLiveDomainResult{}, err
	}
	l, b, _, err = d.audit(ctx)
	if err != nil {
		return ManagedLiveDomainResult{}, err
	}
	return d.finishPending(ctx, l, b)
}

// Resume reconciles one authenticated pending operation. It does not rerun a
// prune once its intent is durable and never calls a previous generation handler.
func (d *ManagedLiveDomains) Resume(ctx context.Context) error {
	if !d.mu.TryLock() {
		return ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	l, b, _, err := d.audit(ctx)
	if err != nil {
		return err
	}
	if l.Binding == nil {
		if err = d.bindDomains(ctx, l, b); err != nil {
			return err
		}
		l, err = d.readLedger(ctx, d.storage.syncBackend)
		if err != nil {
			return err
		}
	}
	if l.Pending != "" {
		_, err = d.finishPending(ctx, l, b)
	}
	if err == nil {
		d.uncertain = false
	}
	return err
}

// DomainEvidence returns a deep JSON copy for diagnostics without exposing a
// mutable retained request or a cache/backend that could bypass the gate.
func (d *ManagedLiveDomains) DomainEvidence(ctx context.Context) ([]byte, error) {
	if !d.mu.TryLock() {
		return nil, ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	l, _, _, err := d.audit(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(l)
}
