package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"ponder.local/ponder-go/checkpoint"
)

const managedLiveRecoveryVersion = "managed-live-recovery-v3"
const managedLiveRecoveryReceiptVersion = "managed-live-recovery-receipt-v2"

func (m *ManagedLive) version() string {
	if m.opts.runtimeOmnichain {
		return managedLiveOmnichainVersion
	}
	if m.opts.runtimeFactories {
		return managedLiveFactoryVersion
	}
	if m.opts.runtimeInitial {
		return managedLiveSourceVersion
	}
	if m.opts.runtimeSetup {
		return managedLiveSetupVersion
	}
	if m.opts.runtimeCachePolicy != nil {
		return managedLiveUncachedVersion
	}
	if m.opts.liveContinuous {
		return managedLiveContinuousVersion
	}
	if m.opts.liveRuntime {
		return managedLiveRuntimeVersion
	}
	if m.opts.coordinated {
		return managedLiveDomainsVersion
	}
	if m.opts.recovery {
		return managedLiveRecoveryVersion
	}
	return managedLiveVersion
}

// OpenManagedLiveRecoveryIndependentPostgres opens the separate recovery format.
func OpenManagedLiveRecoveryIndependentPostgres(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions) (*IndependentStorage, error) {
	opts, err := normalizeManagedLiveOptions(o, in)
	if err != nil {
		return nil, err
	}
	opts.recovery = true
	return openIndependentPostgres(ctx, o, &managedStorageOptions{ManagedFiniteOptions: opts.ManagedFiniteOptions, live: &opts})
}

// OpenManagedLiveRecoveryIndependentPGlite opens the separate recovery format.
func OpenManagedLiveRecoveryIndependentPGlite(ctx context.Context, o IndependentStorageOptions, in ManagedLiveOptions) (*IndependentStorage, error) {
	if o.BusinessDirectory == "" || o.SyncDirectory == "" || o.BusinessDirectory == "memory://" || o.SyncDirectory == "memory://" {
		return nil, fmt.Errorf("managed live recovery requires two persistent filesystem directories")
	}
	opts, err := normalizeManagedLiveOptions(o, in)
	if err != nil {
		return nil, err
	}
	opts.recovery = true
	return openIndependentPGlite(ctx, o, &managedStorageOptions{ManagedFiniteOptions: opts.ManagedFiniteOptions, live: &opts})
}

// A recovery receipt archives the entire unsafe global suffix, including empty
// segments, and binds its old owner, finality, public projection and fresh inputs.
// This format never adopts v1 databases. Sync cache is not read or mutated here:
// callers must acquire replay inputs afresh; runtime sync pruning remains separate.
type managedLiveRecoveryEvidence struct {
	Version       string                     `json:"version"`
	OpenID        string                     `json:"openId"`
	BeforeOwner   string                     `json:"beforeOwner"`
	Heads         map[uint64]ManagedLiveHead `json:"heads"`
	BeforeControl ManagedLiveControl         `json:"beforeControl"`
	BeforeStates  []managedLiveCursorState   `json:"beforeStates"`
	Removed       []ManagedLiveSegment       `json:"removed"`
	UndoJSON      []string                   `json:"undoJSON"`
	AfterControl  ManagedLiveControl         `json:"afterControl"`
}

// Replay completion is a per-chain HEIGHT boundary. Orphan timestamps are not
// a processing target: a valid replacement at the same bounded end may be older.
func liveReplayReached(head ManagedLiveHead, target string) bool {
	_, at, err := normalizeLiveHead(head)
	saved, e := checkpoint.Decode(target)
	return err == nil && e == nil && saved != checkpoint.Max &&
		(saved == checkpoint.Block(saved.BlockTimestamp, saved.ChainID, saved.BlockNumber, true) || saved == checkpoint.Block(saved.BlockTimestamp, saved.ChainID, saved.BlockNumber, false)) &&
		at.ChainID == saved.ChainID && at.BlockNumber >= saved.BlockNumber
}

// A partially replayed chain keeps its old public head. Once the actual block
// reaches that chain's saved height, publish its real checkpoint atomically.
// Both SQL execution and historical replay use the BEFORE-batch targets.
func liveReplayPublishes(head ManagedLiveHead, targets map[uint64]string) bool {
	_, cp, err := normalizeLiveHead(head)
	if err != nil {
		return false
	}
	target, exists := targets[cp.ChainID]
	return !exists || liveReplayReached(head, target)
}

func liveReplayComplete(cursors map[uint64]ManagedLiveHead, targets map[uint64]string) bool {
	for id, target := range targets {
		cp, err := checkpoint.Decode(target)
		if err != nil || cp.ChainID != id || !liveReplayReached(cursors[id], target) {
			return false
		}
	}
	return true
}

func planLiveRecovery(opts ManagedLiveOptions, c ManagedLiveControl, baseline map[uint64]ManagedLiveHead, active []ManagedLiveSegment, journal []managedLiveJournalEntry, states []managedLiveCursorState, owner string) (managedLiveRecoveryEvidence, error) {
	var e managedLiveRecoveryEvidence
	if !opts.recovery || c.Version != (&ManagedLive{opts: opts}).version() || validateLiveControl(c) != nil || c.Batches == 0 || c.Generation >= math.MaxInt64-1 || c.Recoveries == math.MaxUint64 || !liveID(opts.OpenID) || owner == "" || len(states) != len(opts.Chains) || len(opts.FinalizedHeads) != len(opts.Chains) {
		return e, ErrManagedLivePendingActions
	}
	if err := liveActiveJournal(c, active, journal); err != nil {
		return e, err
	}
	if opts.runtimeOmnichain {
		watermark := ""
		for _, head := range opts.FinalizedHeads {
			if watermark == "" || head.Checkpoint < watermark {
				watermark = head.Checkpoint
			}
		}
		for id, head := range c.Cursors {
			cp, err := checkpoint.Decode(head.Checkpoint)
			fresh, fe := checkpoint.Decode(opts.FinalizedHeads[id].Checkpoint)
			if err != nil || fe != nil {
				return e, ErrManagedLiveBatchConflict
			}
			if cp.BlockNumber >= opts.runtimeStarts[id] && !liveDown(c.LogicalCursors[id]) && (head.Checkpoint > watermark || cp.BlockNumber > fresh.BlockNumber) {
				return e, fmt.Errorf("omnichain executed cursor exceeds fresh global finalized watermark")
			}
		}
	}
	safe, finalized := liveSafeHeads(c, baseline), livePublicFinality(c, baseline)
	byChain := map[uint64]managedLiveCursorState{}
	for _, state := range states {
		if _, exists := byChain[state.ChainID]; exists {
			return e, ErrManagedLiveBatchConflict
		}
		byChain[state.ChainID] = state
	}
	targets := map[uint64]string{}
	cuts := map[uint64]uint64{}
	for _, chain := range opts.Chains {
		state, ok := byChain[chain.ID]
		head, fresh, err := normalizeLiveHead(opts.FinalizedHeads[chain.ID])
		_, prior, pe := normalizeLiveHead(finalized[chain.ID])
		latest, le := checkpoint.Decode(state.Latest)
		// Latest is the authenticated public projection, possibly stale across a
		// fork/global rewind. Neither timestamp nor height orders it against
		// finality; complete durable replay already proved both exact identities.
		if !ok || state.Name != chain.Name || state.Finalized != liveFinalCheckpoint(c, baseline, chain.ID) || state.Safe != liveSafeCheckpoint(c, baseline, chain.ID) || state.Cursor != liveCursorCheckpoint(c, chain.ID) || le != nil || latest.ChainID != chain.ID || latest == checkpoint.Max || (latest != checkpoint.Block(latest.BlockTimestamp, latest.ChainID, latest.BlockNumber, true) && !(opts.runtimeInitial && latest == checkpoint.Block(latest.BlockTimestamp, latest.ChainID, latest.BlockNumber, false)) && !(opts.runtimeOmnichain && livePositionForHead(state.Latest, c.Cursors[chain.ID], true))) || prior.ChainID != chain.ID || err != nil || pe != nil || head != opts.FinalizedHeads[chain.ID] || fresh.ChainID != chain.ID {
			return e, ErrManagedLiveBatchConflict
		}
		if fresh.BlockNumber < prior.BlockNumber || fresh.BlockTimestamp < prior.BlockTimestamp {
			return e, ErrManagedLiveFinalityRegression
		}
		if fresh.BlockNumber == prior.BlockNumber && head != finalized[chain.ID] {
			return e, ErrManagedLiveBatchConflict
		}
		targets[chain.ID] = state.Latest
		cuts[chain.ID] = min(fresh.BlockNumber, opts.EndBlocks[chain.ID])
	}
	after := c
	after.Generation++
	if opts.liveRuntime {
		after.Serving = false
	}
	after.Recoveries++
	after.LastOperation = opts.OpenID
	after.Tail = c.SafeOrdinal
	after.Cursors = cloneLiveHeads(safe)
	if opts.runtimeInitial {
		after.LogicalCursors = cloneLiveCheckpoints(c.LogicalSafe)
	}
	after.FinalizedHeads = cloneLiveHeads(opts.FinalizedHeads)
	after.HistoryCuts = cuts
	after.ReplayTargets = targets
	if liveControlReplayComplete(after, targets) {
		after.ReplayTargets = nil
	}
	if validateLiveControl(after) != nil {
		return e, ErrManagedLiveBatchConflict
	}
	e = managedLiveRecoveryEvidence{Version: managedLiveRecoveryReceiptVersion, OpenID: opts.OpenID, BeforeOwner: owner, Heads: cloneLiveHeads(opts.FinalizedHeads), BeforeControl: c, BeforeStates: append([]managedLiveCursorState(nil), states...), Removed: append([]ManagedLiveSegment(nil), active...), UndoJSON: []string{}, AfterControl: after}
	sort.Slice(e.BeforeStates, func(i, j int) bool { return e.BeforeStates[i].ChainID < e.BeforeStates[j].ChainID })
	for _, row := range journal {
		raw, err := json.Marshal(row)
		if err != nil {
			return e, err
		}
		e.UndoJSON = append(e.UndoJSON, string(raw))
	}
	return e, nil
}

func decodeLiveRecoveryUndo(e managedLiveRecoveryEvidence) ([]managedLiveJournalEntry, error) {
	rows := make([]managedLiveJournalEntry, len(e.UndoJSON))
	for i, raw := range e.UndoJSON {
		if err := decodeLiveJSON([]byte(raw), &rows[i]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (m *ManagedLive) recoveryHash(e managedLiveRecoveryEvidence, owner string) string {
	return managedHash(struct {
		Identity, Owner string
		Recovery        managedLiveRecoveryEvidence
	}{m.owner.identity, owner, e})
}

func (m *ManagedLive) prepareRecovery(ctx context.Context, tx Transaction) error {
	d, before, err := m.readDurableState(ctx, tx, true)
	if err != nil {
		return err
	}
	for _, op := range d.Operations {
		if op.ID != m.opts.OpenID {
			continue
		}
		if op.Kind == "open" && op.RequestHash == m.requestHash() {
			return ErrManagedLiveOpenIDUsed
		}
		if op.Kind == "recover" {
			var e managedLiveRecoveryEvidence
			if decodeLiveJSON(op.Payload, &e) == nil && managedHash(e.Heads) == managedHash(m.opts.FinalizedHeads) {
				return ErrManagedLiveOpenIDUsed
			}
		}
		return ErrManagedLiveReceiptConflict
	}
	baseline, err := liveDurableBaselines(d.Operations)
	if err != nil {
		return err
	}
	ordered, err := orderLiveOperations(d.Operations, d.Control.LastOperation)
	if err != nil {
		return err
	}
	e, err := planLiveRecovery(m.opts, d.Control, baseline, d.Segments, d.Journal, d.States, ordered[len(ordered)-1].OwnerToken)
	if err != nil {
		return err
	}
	m.previous, m.beforeHash, m.recovery = &d.Control, before, &e
	return nil
}

// The enclosing owner-open transaction includes undo, private rewind, new lease,
// control and receipt. A failed commit is reconciled by exact snapshot readback.
func (m *ManagedLive) recordRecovery(ctx context.Context, tx Transaction) error {
	e := *m.recovery
	rows, err := decodeLiveRecoveryUndo(e)
	if err != nil {
		return err
	}
	s := m.owner.s
	if err := prepareLiveQueryChanges(ctx, tx, s.Backend.Dialect()); err != nil {
		return err
	}
	if err := tx.Exec(ctx, `SET LOCAL "ponder_go.suppress_notify"='1'`); err != nil {
		return err
	}
	for _, chain := range m.opts.Chains {
		if err := s.eventContext(ctx, tx, liveCursorCheckpoint(e.AfterControl, chain.ID), chain.ID, true); err != nil {
			return err
		}
		break
	}
	if _, err := s.undoJournal(ctx, tx, liveReorgUndoRows(rows)); err != nil {
		return err
	}
	if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE operation_id>$1", e.AfterControl.SafeJournalBoundary); err != nil {
		return err
	}
	if err := tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_live_segments")+" WHERE ordinal>$1", e.AfterControl.SafeOrdinal); err != nil {
		return err
	}
	for _, chain := range m.opts.Chains {
		cp, err := checkpoint.Decode(liveCursorCheckpoint(e.AfterControl, chain.ID))
		if err != nil {
			return err
		}
		if err := s.runtimeCursor(ctx, tx, cp); err != nil {
			return err
		}
	}
	if err := s.createJournalTriggers(ctx, tx); err != nil {
		return err
	}
	e.AfterControl.CatalogHash, err = m.catalogHash(ctx, tx)
	if err != nil {
		return err
	}
	controlJSON, err := json.Marshal(e.AfterControl)
	if err != nil {
		return err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_control")+" SET payload=$1::jsonb WHERE key='control'", string(controlJSON)); err != nil {
		return err
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := tx.Exec(ctx, "INSERT INTO "+s.internal("_ponder_go_live_operations")+" (id,generation,owner_token,kind,request_hash,previous_operation,before_hash,after_hash,payload) VALUES($1,$2,$3,'recover',$4,$5,$6,'',$7::jsonb)", e.OpenID, e.AfterControl.Generation, s.leaseToken, m.recoveryHash(e, s.leaseToken), e.BeforeControl.LastOperation, m.beforeHash, string(payload)); err != nil {
		return err
	}
	after, err := m.snapshotHash(ctx, tx, e.OpenID)
	if err != nil {
		return err
	}
	if err := tx.Exec(ctx, "UPDATE "+s.internal("_ponder_go_live_operations")+" SET after_hash=$1 WHERE id=$2", after, e.OpenID); err != nil {
		return err
	}
	m.opened, m.recovery = e.AfterControl, &e
	_, _, err = m.readDurable(ctx, tx)
	return err
}
