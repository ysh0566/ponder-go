package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

type managedRuntimeStage struct{ Name, Hash, JSON string }
type managedRuntimeServe struct {
	Version, ID, SyncHead string
	Generation            uint64
	Ready                 bool
	Before                ManagedLiveControl
	Heads                 map[uint64]ManagedLiveHead
	Stages                []managedRuntimeStage
	LogOnlyScan           map[uint64]managedLogOnlyScan `json:"logOnlyScan,omitempty"`
	After                 ManagedLiveControl
}

func (m *ManagedLive) planRuntimeServe(before ManagedLiveControl, e managedRuntimeServe) (ManagedLiveControl, error) {
	after := before
	expectedVersion := "runtime-serve-v1"
	if logOnlyOptions(m.opts) {
		expectedVersion = "runtime-serve-logonly-v16"
	} else if len(e.LogOnlyScan) != 0 {
		return after, ErrManagedLiveBatchConflict
	}
	if !m.opts.liveRuntime || e.Version != expectedVersion || !liveID(e.ID) || e.ID == before.LastOperation || e.Generation != before.Generation || managedHash(e.Before) != managedHash(before) || !liveDigest(e.SyncHead) || e.Ready == before.Serving {
		return after, ErrManagedLiveBatchConflict
	}
	if e.Ready {
		if before.Batches == 0 || len(before.ReplayTargets) != 0 || before.Activations == math.MaxUint64 || len(e.Heads) != len(m.opts.Chains) {
			return after, ErrManagedLivePendingActions
		}
		for _, chain := range m.opts.Chains {
			head, ok := e.Heads[chain.ID]
			normalized, _, err := normalizeLiveHead(head)
			if !ok || err != nil || head != normalized || head != before.Cursors[chain.ID] || (!logOnlyOptions(m.opts) && !ManagedLiveReachedBlock(before, chain.ID, before.HistoryCuts[chain.ID])) {
				return after, ErrManagedLivePendingActions
			}
		}
		if logOnlyOptions(m.opts) {
			ends, err := m.validateLogOnlyScan(before, e.LogOnlyScan)
			if err != nil {
				return after, err
			}
			after.LogOnlyScanHeads = ends
		}
		expected := m.owner.activationStages()
		if len(expected) != len(e.Stages) {
			return after, ErrManagedLiveBatchConflict
		}
		for i, stage := range e.Stages {
			var value any
			if stage.Name != expected[i].name || json.Unmarshal([]byte(stage.JSON), &value) != nil || stage.Hash != managedHash(value) {
				return after, ErrManagedLiveBatchConflict
			}
		}
		after.Activations++
	} else if len(e.LogOnlyScan) != 0 || len(e.Heads) != 0 || len(e.Stages) != 0 || e.After.CatalogHash != before.CatalogHash {
		return after, ErrManagedLiveBatchConflict
	}
	after.Serving = e.Ready
	after.LastOperation = e.ID
	after.CatalogHash = e.After.CatalogHash
	if !liveDigest(after.CatalogHash) || validateLiveControl(after) != nil {
		return after, ErrManagedLiveBatchConflict
	}
	return after, nil
}

func (m *ManagedLive) runtimeServeHash(e managedRuntimeServe, owner string) string {
	return managedHash(struct {
		Identity, Owner string
		Evidence        managedRuntimeServe
	}{m.owner.identity, owner, e})
}

func (d *ManagedLiveDomains) ActivateRuntime(ctx context.Context, id string, heads map[uint64]ManagedLiveHead) error {
	return d.setRuntimeServing(ctx, id, true, heads)
}
func (d *ManagedLiveDomains) PauseRuntime(ctx context.Context, id string) error {
	return d.setRuntimeServing(ctx, id, false, nil)
}
func (d *ManagedLiveDomains) setRuntimeServing(ctx context.Context, id string, ready bool, heads map[uint64]ManagedLiveHead) error {
	return d.setRuntimeServingWithScan(ctx, id, ready, heads, nil)
}
func (d *ManagedLiveDomains) setRuntimeServingWithScan(ctx context.Context, id string, ready bool, heads map[uint64]ManagedLiveHead, lineage map[uint64][]ManagedLiveHead) error {
	if !d.mu.TryLock() {
		return ErrManagedLiveBusy
	}
	defer d.mu.Unlock()
	if err := d.gate(ctx); err != nil {
		return err
	}
	m := d.live()
	if !m.opts.liveRuntime {
		return ErrManagedLiveLegacyMutation
	}
	if err := d.runtimeQuiescent(); err != nil {
		return err
	}
	l, b, _, err := d.audit(ctx)
	if err != nil {
		return err
	}
	for _, op := range b.Operations {
		if op.ID == id {
			var e managedRuntimeServe
			if op.Kind != "serve" || decodeLiveJSON(op.Payload, &e) != nil || e.Ready != ready || managedHash(e.Heads) != managedHash(heads) || e.Generation != m.opened.Generation || op.OwnerToken != m.owner.s.leaseToken || (logOnlyOptions(m.opts) && !logOnlySameLineage(e.LogOnlyScan, lineage)) || (!logOnlyOptions(m.opts) && lineage != nil) {
				return ErrManagedLiveReceiptConflict
			}
			return nil
		}
	}
	d.servingPublication.Store(false)
	e := managedRuntimeServe{Version: "runtime-serve-v1", ID: id, SyncHead: l.Head, Generation: b.Control.Generation, Ready: ready, Before: b.Control, Heads: cloneLiveHeads(heads)}
	if logOnlyOptions(m.opts) {
		e.Version = "runtime-serve-logonly-v16"
		if ready {
			e.LogOnlyScan, err = d.logOnlyScan(ctx, b.Control, lineage)
			if err != nil {
				return err
			}
		}
	} else if lineage != nil {
		return ErrManagedLiveBatchConflict
	}
	if !ready {
		e.Heads = nil
	}
	s := m.owner.s
	if !s.mu.TryLock() {
		return ErrManagedLiveBusy
	}
	defer s.mu.Unlock()
	tx, err := s.Backend.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = s.lock(ctx, tx); err != nil {
		return err
	}
	if err = m.owner.requireLease(ctx, tx); err != nil {
		return err
	}
	before, beforeHash, err := m.readDurable(ctx, tx)
	if err != nil {
		return err
	}
	if managedHash(before.Control) != managedHash(b.Control) {
		return ErrManagedLiveBatchConflict
	}
	if ready {
		// Validate targets BEFORE DDL. Catalog descriptors are filled only from the
		// actual stage SQL below; a caller cannot supply a ready/catalog shortcut.
		if before.Control.Batches == 0 || before.Control.Serving || len(before.Control.ReplayTargets) != 0 || len(heads) != len(m.opts.Chains) {
			return ErrManagedLivePendingActions
		}
		for _, chain := range m.opts.Chains {
			h, ok := heads[chain.ID]
			_, _, he := normalizeLiveHead(h)
			if !ok || he != nil || h != before.Control.Cursors[chain.ID] || (!logOnlyOptions(m.opts) && !ManagedLiveReachedBlock(before.Control, chain.ID, before.Control.HistoryCuts[chain.ID])) {
				return ErrManagedLivePendingActions
			}
		}
		if logOnlyOptions(m.opts) {
			if _, err = m.validateLogOnlyScan(before.Control, e.LogOnlyScan); err != nil {
				return err
			}
		}
		for _, stage := range m.owner.activationStages() {
			present, err := m.owner.verifyStage(ctx, tx, stage)
			if err != nil {
				return err
			}
			if !present {
				// Journal capture is already required by every executed native block.
				// Other stages must be genuinely absent before their first activation.
				if stage.name != "capture" {
					if err = m.owner.requireStageAbsent(ctx, tx, stage.name); err != nil {
						return err
					}
					if err = stage.apply(ctx, tx); err != nil {
						return err
					}
				}
				value, err := stage.catalog(ctx, tx)
				if err != nil {
					return err
				}
				if err = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_managed_stages")+"(name,owner_token,catalog_hash) VALUES($1,$2,$3)", stage.name, s.leaseToken, managedHash(value)); err != nil {
					return err
				}
			}
			value, err := stage.catalog(ctx, tx)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			e.Stages = append(e.Stages, managedRuntimeStage{Name: stage.name, Hash: managedHash(value), JSON: string(raw)})
		}
	}
	e.After = e.Before
	e.After.CatalogHash, err = m.catalogHash(ctx, tx)
	if err != nil {
		return err
	}
	after, err := m.planRuntimeServe(before.Control, e)
	if err != nil {
		return err
	}
	e.After = after
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	control, err := json.Marshal(after)
	if err != nil {
		return err
	}
	if err = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_control")+" SET payload=$1::jsonb WHERE key='control'", string(control)); err != nil {
		return err
	}
	flag := 0
	if ready {
		flag = 1
	}
	if err = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{is_ready}',to_jsonb($1::integer)) WHERE key='app' AND lease_token=$2", flag, s.leaseToken); err != nil {
		return err
	}
	hash := m.runtimeServeHash(e, s.leaseToken)
	if err = tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_operations")+"(id,generation,owner_token,kind,request_hash,previous_operation,before_hash,after_hash,payload) VALUES($1,$2,$3,'serve',$4,$5,$6,'',$7::jsonb)", id, after.Generation, s.leaseToken, hash, before.Control.LastOperation, beforeHash, string(payload)); err != nil {
		return err
	}
	afterHash, err := m.snapshotHash(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_operations")+" SET after_hash=$1 WHERE id=$2", afterHash, id); err != nil {
		return err
	}
	if _, _, err = m.readDurable(ctx, tx); err != nil {
		return err
	}
	if err = m.owner.requireLease(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err == nil {
		d.servingPublication.Store(ready)
		return nil
	}
	commitErr := err
	_ = tx.Rollback(context.Background())
	read, err := s.Backend.Begin(ctx)
	if err == nil {
		defer read.Rollback(context.Background())
		if err = m.owner.requireLease(ctx, read); err == nil {
			var actual managedLiveDurable
			actual, _, err = m.readDurable(ctx, read)
			if err == nil {
				for _, op := range actual.Operations {
					if op.ID == id && op.Kind == "serve" && op.RequestHash == hash && op.OwnerToken == s.leaseToken {
						d.servingPublication.Store(ready)
						return nil
					}
				}
				err = fmt.Errorf("exact serve receipt not confirmed")
			}
		}
	}
	m.uncertain.Store(true)
	return &ManagedStageError{Stage: "runtime-serve", Outcome: "unknown", Err: errors.Join(commitErr, err)}
}

func (m *ManagedLive) validateRuntimeServing(ctx context.Context, q Querier, c ManagedLiveControl, token string) error {
	r, err := q.Query(ctx, "SELECT value->>'is_ready' AS ready FROM "+m.owner.s.internal("_ponder_go_app")+" WHERE key='app'")
	if err != nil {
		return err
	}
	flag := "0"
	if c.Serving {
		flag = "1"
	}
	if len(r.Rows) != 1 || r.Rows[0]["ready"] != flag {
		return ErrManagedLiveBatchConflict
	}
	if c.Serving {
		for _, stage := range m.owner.activationStages() {
			r, err := q.Query(ctx, "SELECT owner_token,catalog_hash FROM "+m.owner.s.internal("_ponder_go_managed_stages")+" WHERE name=$1", stage.name)
			if err != nil {
				return err
			}
			value, err := stage.catalog(ctx, q)
			if err != nil {
				return err
			}
			if len(r.Rows) != 1 || r.Rows[0]["owner_token"] != token || r.Rows[0]["catalog_hash"] != managedHash(value) {
				return fmt.Errorf("runtime active catalog stage %s differs: %w", stage.name, ErrManagedLivePendingActions)
			}
		}
	}
	return nil
}
func (m *ManagedLive) runtimeReady(ctx context.Context) (bool, error) {
	c, err := m.Control(ctx)
	if err != nil {
		return false, err
	}
	a, err := m.owner.Metadata(ctx)
	if err != nil {
		return false, err
	}
	return c.Serving && a.IsReady == 1 && a.IsLocked == 1, nil
}
