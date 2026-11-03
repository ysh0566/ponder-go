package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"ponder.local/ponder-go/checkpoint"
)

const managedLiveReorgVersion = "managed-live-reorg-v1"

var ErrManagedLiveDeepReorg = errors.New("managed live reorg crosses the durable safe boundary")

// ManagedLiveReorg names one exact, already-acquired ancestor on the current
// executed branch. A retained proof above a rewound cursor is not an ancestor.
type ManagedLiveReorg struct {
	ID                    string          `json:"id"`
	Generation            uint64          `json:"generation"`
	ExpectedLastOperation string          `json:"expectedLastOperation"`
	ExpectedTail          uint64          `json:"expectedTail"`
	Ancestor              ManagedLiveHead `json:"ancestor"`
}

type ManagedLiveReorgAcquisition struct {
	owner       *ManagedLive
	generation  uint64
	requestHash string
}

// This result concerns the business domain only. An orchestrator must next
// prune affected sync provenance/RPC cache in its OWN transaction, keyed to this
// durable operation, before consuming Replay. No distributed commit is claimed.
// Removed may be empty; Applied reports a receipt commit, not sync completion.
// Sync pruning and runtime orchestration are not implemented here.
type ManagedLiveReorgResult struct {
	Applied     bool                       `json:"applied"`
	OperationID string                     `json:"operationId"`
	Removed     []ManagedLiveSegment       `json:"removed"`
	Replay      []ManagedLiveBlock         `json:"replay"`
	Cursors     map[uint64]ManagedLiveHead `json:"cursors"`
	SyncRewind  ManagedLiveHead            `json:"syncRewind"`
}

type managedLiveReorgPlan struct {
	After           ManagedLiveControl
	Removed         []ManagedLiveSegment
	Undo            []managedLiveJournalEntry // Ascending numeric journal ID.
	Replay          []ManagedLiveBlock
	Proofs          []ManagedLiveHead
	JournalBoundary int64
}

type managedLiveReorgEvidence struct {
	Version         string                     `json:"version"`
	Request         ManagedLiveReorg           `json:"request"`
	AcquisitionHash string                     `json:"acquisitionHash"`
	BeforeCursors   map[uint64]ManagedLiveHead `json:"beforeCursors"`
	Removed         []ManagedLiveSegment       `json:"removed"`
	// Each row is JSON TEXT inside the outer jsonb receipt. This preserves the
	// key order of RawMessage fields bound by an existing journal digest.
	UndoJSON     []string           `json:"undoJSON"`
	Replay       []ManagedLiveBlock `json:"replay"`
	AfterControl ManagedLiveControl `json:"afterControl"`
}

func normalizeLiveReorg(opts ManagedLiveOptions, in ManagedLiveReorg) (ManagedLiveReorg, error) {
	if !liveID(in.ID) || !liveID(in.ExpectedLastOperation) || in.ID == in.ExpectedLastOperation || in.Generation == 0 || in.Generation >= math.MaxInt64 || in.ExpectedTail >= math.MaxInt64-1 {
		return in, ErrManagedLiveBatchConflict
	}
	head, cp, err := normalizeLiveHead(in.Ancestor)
	if err != nil {
		return in, err
	}
	for _, chain := range opts.Chains {
		if chain.ID == cp.ChainID {
			in.Ancestor = head
			return in, nil
		}
	}
	return in, ErrManagedLiveBatchConflict
}

func (m *ManagedLive) reorgHash(request ManagedLiveReorg) string {
	return liveReorgHash(m.owner.identity, m.owner.s.leaseToken, request)
}

func liveReorgHash(identity, owner string, request ManagedLiveReorg) string {
	return managedHash(struct {
		Identity, Owner string
		Reorg           ManagedLiveReorg
	}{identity, owner, request})
}

func (m *ManagedLive) RetainReorg(ctx context.Context, in ManagedLiveReorg) (*ManagedLiveReorgAcquisition, error) {
	if err := m.usable(ctx); err != nil {
		return nil, err
	}
	r, err := normalizeLiveReorg(m.opts, in)
	if err != nil || r.Generation != m.opened.Generation {
		return nil, errors.Join(ErrManagedLiveBatchConflict, err)
	}
	return &ManagedLiveReorgAcquisition{owner: m, generation: r.Generation, requestHash: m.reorgHash(r)}, nil
}

func (m *ManagedLive) requireReorgAcquisition(r ManagedLiveReorg, acquired *ManagedLiveReorgAcquisition) error {
	if acquired == nil || acquired.owner != m || acquired.generation != r.Generation || acquired.generation != m.opened.Generation || acquired.requestHash != m.reorgHash(r) {
		return ErrManagedLiveBatchConflict
	}
	return nil
}

func copyLiveJournal(rows []managedLiveJournalEntry) []managedLiveJournalEntry {
	out := make([]managedLiveJournalEntry, len(rows))
	for i, row := range rows {
		out[i] = row
		out[i].Key = append(json.RawMessage(nil), row.Key...)
		out[i].Before = append(json.RawMessage(nil), row.Before...)
		out[i].Row = append(json.RawMessage(nil), row.Row...)
	}
	return out
}

// Partition only this physical active history or one archived removed suffix.
// Combining all historical row IDs into a range would mix old forks on replay.
func partitionLiveJournal(segments []ManagedLiveSegment, rows []managedLiveJournalEntry, boundary int64) (map[uint64][]managedLiveJournalEntry, error) {
	out := map[uint64][]managedLiveJournalEntry{}
	position := 0
	var previous uint64
	for i, segment := range segments {
		if segment.Ordinal == 0 || (i > 0 && (segment.Ordinal <= previous || segment.Predecessor != previous)) || segment.Journal.Before != boundary || segment.Journal.After < boundary {
			return nil, fmt.Errorf("managed segment/journal partition differs")
		}
		end := position
		for end < len(rows) && rows[end].ID <= segment.Journal.After {
			end++
		}
		span, err := liveJournalSpan(boundary, segment.Block.Head, rows[position:end])
		if err != nil || span != segment.Journal {
			return nil, errors.Join(err, fmt.Errorf("managed archived journal span/digest differs"))
		}
		out[segment.Ordinal] = rows[position:end]
		boundary, position, previous = span.After, end, segment.Ordinal
	}
	if position != len(rows) {
		return nil, fmt.Errorf("managed journal has unbound rows")
	}
	return out, nil
}

func planLiveReorg(opts ManagedLiveOptions, c ManagedLiveControl, baselines map[uint64]ManagedLiveHead, active []ManagedLiveSegment, journal []managedLiveJournalEntry, proofs []ManagedLiveHead, r ManagedLiveReorg) (managedLiveReorgPlan, error) {
	var plan managedLiveReorgPlan
	if opts.runtimeOmnichain {
		return plan, fmt.Errorf("bounded finalized omnichain has no live reorg transition")
	}
	if err := validateLiveControl(c); err != nil {
		return plan, err
	}
	if r.Generation != c.Generation || r.ExpectedLastOperation != c.LastOperation || r.ExpectedTail != c.Tail || c.Batches == 0 || c.Reorgs == math.MaxUint64 {
		return plan, ErrManagedLiveBatchConflict
	}
	ancestor, cp, err := normalizeLiveHead(r.Ancestor)
	safeHeads := liveSafeHeads(c, baselines)
	baseline, ok := safeHeads[cp.ChainID]
	base, be := checkpoint.Decode(baseline.Checkpoint)
	current, ce := checkpoint.Decode(c.Cursors[cp.ChainID].Checkpoint)
	if err != nil || ancestor != r.Ancestor || !ok || be != nil || ce != nil || len(baselines) != len(opts.Chains) {
		return plan, ErrManagedLiveBatchConflict
	}
	if cp.BlockNumber < base.BlockNumber || cp.BlockTimestamp < base.BlockTimestamp {
		return plan, ErrManagedLiveDeepReorg
	}
	if c.Finalizations > 0 {
		finalized := livePublicFinality(c, baselines)[cp.ChainID]
		_, finalCP, err := normalizeLiveHead(finalized)
		if err != nil {
			return plan, ErrManagedLiveBatchConflict
		}
		if cp.BlockNumber < finalCP.BlockNumber || cp.BlockTimestamp < finalCP.BlockTimestamp {
			return plan, ErrManagedLiveDeepReorg
		}
		if err := validateLiveFinalityBranch(opts, baselines, c.Finality, safeHeads, c.Cursors, active, proofs); err != nil {
			return plan, err
		}
	}
	if safe, exists := c.SafeAnchors[cp.ChainID]; exists {
		safeCP, err := checkpoint.Decode(safe)
		if err != nil || safeCP.ChainID != cp.ChainID {
			return plan, ErrManagedLivePendingActions
		}
		if cp.BlockNumber < safeCP.BlockNumber || cp.BlockTimestamp < safeCP.BlockTimestamp {
			return plan, ErrManagedLiveDeepReorg
		}
	}
	if cp.BlockNumber > current.BlockNumber {
		return plan, ErrManagedLiveBatchConflict
	}
	if err := liveActiveJournal(c, active, journal); err != nil {
		return plan, err
	}
	known := ancestor == baseline
	cut := -1
	for i, segment := range active {
		block, err := checkpoint.Decode(segment.Block.Head.Checkpoint)
		if err != nil || segment.Ordinal >= c.Next {
			return plan, ErrManagedLiveBatchConflict
		}
		if segment.Block.Head == ancestor {
			known = true
		}
		if cut < 0 && block.ChainID == cp.ChainID && block.BlockNumber > cp.BlockNumber {
			cut = i
		}
	}
	if !known {
		return plan, ErrManagedLiveBatchConflict
	}
	if cut < 0 {
		if ancestor != c.Cursors[cp.ChainID] {
			return plan, ErrManagedLiveBatchConflict
		}
		// No orphaned business segment still records the separate sync rewind intent.
		cut = len(active)
	}
	keep := c.SafeOrdinal
	plan.JournalBoundary = c.SafeJournalBoundary
	if cut > 0 {
		keep = active[cut-1].Ordinal
		plan.JournalBoundary = active[cut-1].Journal.After
	}
	if keep < c.SafeOrdinal {
		return plan, ErrManagedLiveDeepReorg
	}
	plan.Removed = append([]ManagedLiveSegment(nil), active[cut:]...)
	cursors := cloneLiveHeads(c.Cursors)
	positions := cloneLiveCheckpoints(c.LogicalCursors)
	for i := len(plan.Removed) - 1; i >= 0; i-- {
		segment := plan.Removed[i]
		block, _ := checkpoint.Decode(segment.Block.Head.Checkpoint)
		if cursors[block.ChainID] != segment.Block.Head {
			return plan, ErrManagedLiveBatchConflict
		}
		cursors[block.ChainID] = segment.BeforeCursor
		if opts.runtimeInitial {
			positions[block.ChainID] = segment.BeforeCheckpoint
		}
	}
	if cursors[cp.ChainID] != ancestor {
		return plan, ErrManagedLiveBatchConflict
	}
	for _, row := range journal {
		if row.ID > plan.JournalBoundary {
			plan.Undo = append(plan.Undo, row)
		}
	}
	plan.Undo = copyLiveJournal(plan.Undo)
	retained := map[string]ManagedLiveHead{}
	for _, proof := range proofs {
		head, block, err := normalizeLiveHead(proof)
		key := liveProofKey(proof)
		if err != nil || head != proof {
			return plan, ErrManagedLiveBatchConflict
		}
		if _, exists := retained[key]; exists {
			return plan, ErrManagedLiveBatchConflict
		}
		retained[key] = proof
		if block.ChainID != cp.ChainID || block.BlockNumber <= cp.BlockNumber {
			plan.Proofs = append(plan.Proofs, proof)
		}
	}
	if retained[liveProofKey(ancestor)] != ancestor {
		return plan, ErrManagedLiveBatchConflict
	}
	for _, segment := range plan.Removed {
		block, _ := checkpoint.Decode(segment.Block.Head.Checkpoint)
		if block.ChainID != cp.ChainID {
			if retained[liveProofKey(segment.Block.Head)] != segment.Block.Head {
				return plan, ErrManagedLiveBatchConflict
			}
			plan.Replay = append(plan.Replay, segment.Block)
		}
	}
	plan.After = c
	plan.After.Tail, plan.After.LastOperation, plan.After.Cursors = keep, r.ID, cursors
	if opts.runtimeInitial {
		plan.After.LogicalCursors = positions
	}
	plan.After.Reorgs++ // Next is a high-water mark and never rewinds.
	if opts.liveContinuous {
		targets := map[uint64]string{}
		for id := range c.Cursors {
			targets[id] = liveCursorCheckpoint(c, id)
		}
		for id, target := range c.ReplayTargets {
			saved, e := checkpoint.Decode(target)
			current, ce := checkpoint.Decode(targets[id])
			if e != nil || ce != nil || saved.ChainID != id || current.ChainID != id {
				return plan, ErrManagedLiveBatchConflict
			}
			if saved.BlockNumber > current.BlockNumber || (opts.runtimeInitial && saved.BlockNumber == current.BlockNumber && !liveDown(target) && liveDown(targets[id])) {
				targets[id] = target
			}
		}
		plan.After.ReplayTargets = targets
		plan.After.Serving = false
		if liveControlReplayComplete(plan.After, targets) {
			plan.After.ReplayTargets = nil
		}
	}

	return plan, nil
}

func makeLiveReorgEvidence(r ManagedLiveReorg, hash string, before ManagedLiveControl, plan managedLiveReorgPlan) (managedLiveReorgEvidence, error) {
	e := managedLiveReorgEvidence{Version: managedLiveReorgVersion, Request: r, AcquisitionHash: hash, BeforeCursors: cloneLiveHeads(before.Cursors), Removed: plan.Removed, Replay: plan.Replay, AfterControl: plan.After, UndoJSON: []string{}}
	for _, row := range plan.Undo {
		raw, err := json.Marshal(row)
		if err != nil {
			return e, err
		}
		e.UndoJSON = append(e.UndoJSON, string(raw))
	}
	return e, nil
}

func decodeLiveReorgUndo(e managedLiveReorgEvidence) ([]managedLiveJournalEntry, error) {
	rows := make([]managedLiveJournalEntry, len(e.UndoJSON))
	for i, raw := range e.UndoJSON {
		if err := decodeLiveJSON([]byte(raw), &rows[i]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func exactLiveReorgRetry(r ManagedLiveReorg, hash, owner string, op managedLiveOperation) (managedLiveReorgEvidence, error) {
	var e managedLiveReorgEvidence
	if op.Kind != "reorg" || op.ID != r.ID || op.Generation != r.Generation || op.OwnerToken != owner || op.RequestHash != hash || decodeLiveJSON(op.Payload, &e) != nil || e.Version != managedLiveReorgVersion || e.AcquisitionHash != hash || managedHash(e.Request) != managedHash(r) {
		return e, ErrManagedLiveBatchConflict
	}
	return e, nil
}

func liveReorgResult(e managedLiveReorgEvidence, applied bool) ManagedLiveReorgResult {
	return ManagedLiveReorgResult{Applied: applied, OperationID: e.Request.ID, Removed: append([]ManagedLiveSegment(nil), e.Removed...), Replay: append([]ManagedLiveBlock(nil), e.Replay...), Cursors: cloneLiveHeads(e.AfterControl.Cursors), SyncRewind: e.Request.Ancestor}
}
