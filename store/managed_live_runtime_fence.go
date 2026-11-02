package store

import (
	"context"
	"errors"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
)

// This receipt is committed with the RPC deletion, after business recovery and
// before any new fetch epoch is admitted. Raw source data is retained.
type managedRuntimeFence struct {
	Identity, Owner, Operation, BusinessHash string
	Generation                               uint64
	Control                                  ManagedLiveControl
	Cuts                                     map[uint64]uint64
	Deleted                                  []string
}

func runtimeOperationControl(op managedLiveOperation) (ManagedLiveControl, error) {
	var c ManagedLiveControl
	switch op.Kind {
	case "open":
		if err := decodeLiveJSON(op.Payload, &c); err != nil {
			return c, err
		}
	case "recover":
		var e managedLiveRecoveryEvidence
		if err := decodeLiveJSON(op.Payload, &e); err != nil {
			return c, err
		}
		c = e.AfterControl
	default:
		return c, ErrManagedLiveReceiptConflict
	}
	return c, nil
}
func (d *ManagedLiveDomains) runtimeFenceFor(op managedLiveOperation) (managedRuntimeFence, error) {
	c, err := runtimeOperationControl(op)
	if err != nil {
		return managedRuntimeFence{}, err
	}
	f := managedRuntimeFence{Identity: d.live().owner.identity, Owner: op.OwnerToken, Operation: op.ID, BusinessHash: op.AfterHash, Generation: op.Generation, Control: c, Cuts: map[uint64]uint64{}, Deleted: []string{}}
	for _, chain := range d.live().opts.Chains {
		h, ok := c.Cursors[chain.ID]
		if !ok {
			h = c.FinalizedHeads[chain.ID]
		}
		cp, err := checkpoint.Decode(h.Checkpoint)
		if err != nil || cp.ChainID != chain.ID {
			return f, ErrManagedLiveReceiptConflict
		}
		f.Cuts[chain.ID] = min(cp.BlockNumber, d.live().opts.EndBlocks[chain.ID])
	}
	return f, nil
}
func (d *ManagedLiveDomains) checkRuntimeLedger(l liveDomainLedger, b managedLiveDurable) error {
	if !d.live().opts.liveRuntime {
		return nil
	}
	ordered, err := orderLiveOperations(b.Operations, b.Control.LastOperation)
	if err != nil {
		return err
	}
	ops := map[string]managedLiveOperation{}
	for _, op := range ordered {
		ops[op.ID] = op
	}
	fenceGeneration := map[uint64]bool{}
	heads := map[string]bool{managedHash(d.spec): true}
	for _, r := range l.Records {
		if r.Kind == "generation" {
			var actual managedRuntimeFence
			if decodeLiveJSON([]byte(r.Payload), &actual) != nil {
				return ErrManagedLiveReceiptConflict
			}
			op, ok := ops[r.ID]
			if !ok {
				return ErrManagedLiveReceiptConflict
			}
			want, err := d.runtimeFenceFor(op)
			if err != nil {
				return err
			}
			if !runtimeFenceDeletedAllowed(d.live().opts, actual.Cuts, actual.Deleted) {
				return ErrManagedLiveReceiptConflict
			}
			want.Deleted = actual.Deleted
			if managedHash(want) != managedHash(actual) || fenceGeneration[op.Generation] {
				return ErrManagedLiveReceiptConflict
			}
			fenceGeneration[op.Generation] = true
		}
		heads[r.Hash] = true
	}
	for _, op := range ordered {
		if op.Kind != "open" && op.Kind != "recover" && !fenceGeneration[op.Generation] {
			return ErrManagedLiveReceiptConflict
		}
		if op.Kind == "serve" {
			var e managedRuntimeServe
			if decodeLiveJSON(op.Payload, &e) != nil || !heads[e.SyncHead] {
				return ErrManagedLiveReceiptConflict
			}
		}
	}
	return nil
}
func (d *ManagedLiveDomains) ensureRuntimeFence(ctx context.Context) error {
	l, b, _, err := d.audit(ctx)
	if err != nil {
		return err
	}
	var op managedLiveOperation
	for _, v := range b.Operations {
		if v.ID == d.live().opened.LastOperation {
			op = v
			break
		}
	}
	want, err := d.runtimeFenceFor(op)
	if err != nil {
		return err
	}
	for _, r := range l.Records {
		if r.Kind == "generation" && r.ID == op.ID {
			return nil
		}
	}
	err = d.storage.syncCache.transaction(ctx, func(tx Transaction) error {
		actual, e := d.readLedger(ctx, tx)
		if e != nil {
			return e
		}
		if actual.Head != l.Head || actual.Pending != "" {
			return ErrManagedLiveDomainsPending
		}
		for _, chain := range d.live().opts.Chains {
			if !runtimeRPCCacheEnabled(d.live().opts, chain.ID) {
				continue
			}
			cut := want.Cuts[chain.ID]
			table := d.storage.syncCache.table("rpc_request_results")
			rows, e := tx.Query(ctx, "SELECT row_to_json(r)::text AS row FROM "+table+" r WHERE chain_id=$1 AND block_number>$2 ORDER BY request_hash", chain.ID, cut)
			if e != nil {
				return e
			}
			for _, r := range rows.Rows {
				want.Deleted = append(want.Deleted, fmt.Sprint(r["row"]))
			}
			if e = tx.Exec(ctx, "DELETE FROM "+table+" WHERE chain_id=$1 AND block_number>$2", chain.ID, cut); e != nil {
				return e
			}
		}
		return d.appendLedger(ctx, tx, actual, "generation", op.ID, want)
	})
	if err == nil {
		return nil
	}
	actual, e := d.readLedger(ctx, d.storage.syncBackend)
	if e == nil {
		for _, r := range actual.Records {
			if r.Kind == "generation" && r.ID == op.ID {
				var got managedRuntimeFence
				if decodeLiveJSON([]byte(r.Payload), &got) == nil && managedHash(got) == managedHash(want) {
					return d.checkRuntimeLedger(actual, b)
				}
			}
		}
		if actual.Head == l.Head {
			return err
		}
	}
	d.uncertain = true
	return &ManagedStageError{Stage: "runtime-generation-fence", Outcome: "unknown", Err: errors.Join(err, e)}
}
