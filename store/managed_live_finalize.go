package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const managedLiveFinalizeVersion = "managed-live-finalize-v1"

var ErrManagedLiveFinalityRegression = errors.New("managed live finalized checkpoint cannot regress")

// Finalized is an exact subset update of already acquired retained heads. A
// later chain's confirmation never skips an earlier unsafe global segment.
type ManagedLiveFinalize struct {
	ID                    string                     `json:"id"`
	Generation            uint64                     `json:"generation"`
	ExpectedLastOperation string                     `json:"expectedLastOperation"`
	ExpectedTail          uint64                     `json:"expectedTail"`
	ExpectedSafeOrdinal   uint64                     `json:"expectedSafeOrdinal"`
	Finalized             map[uint64]ManagedLiveHead `json:"finalized"`
	SourceCheckpoints     map[uint64]string          `json:"sourceCheckpoints,omitempty"`
}

type ManagedLiveFinalizeAcquisition struct {
	owner       *ManagedLive
	generation  uint64
	requestHash string
}

type ManagedLiveFinalizeResult struct {
	Applied     bool                       `json:"applied"`
	Changed     bool                       `json:"changed"`
	OperationID string                     `json:"operationId"`
	Pruned      []ManagedLiveSegment       `json:"pruned"`
	Finalized   map[uint64]ManagedLiveHead `json:"finalized"`
	Safe        map[uint64]ManagedLiveHead `json:"safe"`
	SafeOrdinal uint64                     `json:"safeOrdinal"`
}

type managedLiveFinalizePlan struct {
	After   ManagedLiveControl
	Pruned  []ManagedLiveSegment
	Journal []managedLiveJournalEntry
	Changed bool
}

type managedLiveFinalizeEvidence struct {
	Version               string                     `json:"version"`
	Request               ManagedLiveFinalize        `json:"request"`
	AcquisitionHash       string                     `json:"acquisitionHash"`
	BeforeSafeHeads       map[uint64]ManagedLiveHead `json:"beforeSafeHeads"`
	BeforeFinality        map[uint64]ManagedLiveHead `json:"beforeFinality"`
	BeforeJournalBoundary int64                      `json:"beforeJournalBoundary"`
	Pruned                []ManagedLiveSegment       `json:"pruned"`
	JournalJSON           []string                   `json:"journalJSON"`
	AfterControl          ManagedLiveControl         `json:"afterControl"`
	Changed               bool                       `json:"changed"`
}

func normalizeLiveFinalize(opts ManagedLiveOptions, in ManagedLiveFinalize) (ManagedLiveFinalize, error) {
	if !liveID(in.ID) || !liveID(in.ExpectedLastOperation) || in.ID == in.ExpectedLastOperation || in.Generation == 0 || in.Generation >= math.MaxInt64 || in.ExpectedTail >= math.MaxInt64-1 || in.ExpectedSafeOrdinal > in.ExpectedTail || len(in.Finalized) == 0 || len(in.Finalized) > len(opts.Chains) {
		return in, ErrManagedLiveBatchConflict
	}
	chains := map[uint64]bool{}
	for _, chain := range opts.Chains {
		chains[chain.ID] = true
	}
	out := in
	if (!opts.runtimeOmnichain && in.SourceCheckpoints != nil) || (opts.runtimeOmnichain && len(in.SourceCheckpoints) != len(in.Finalized)) {
		return out, ErrManagedLiveBatchConflict
	}
	out.SourceCheckpoints = cloneLiveCheckpoints(in.SourceCheckpoints)
	out.Finalized = map[uint64]ManagedLiveHead{}
	for id, head := range in.Finalized {
		normalized, cp, err := normalizeLiveHead(head)
		if err != nil || !chains[id] || cp.ChainID != id || (opts.runtimeOmnichain && (!positionForOptions(in.SourceCheckpoints[id], normalized, opts) || liveDown(in.SourceCheckpoints[id]))) {
			return out, errors.Join(ErrManagedLiveBatchConflict, err)
		}
		out.Finalized[id] = normalized
	}
	return out, nil
}

func (m *ManagedLive) finalizeHash(request ManagedLiveFinalize) string {
	return liveFinalizeHash(m.owner.identity, m.owner.s.leaseToken, request)
}

func liveFinalizeHash(identity, owner string, request ManagedLiveFinalize) string {
	return managedHash(struct {
		Identity, Owner string
		Finalize        ManagedLiveFinalize
	}{identity, owner, request})
}

func (m *ManagedLive) RetainFinalize(ctx context.Context, in ManagedLiveFinalize) (*ManagedLiveFinalizeAcquisition, error) {
	if err := m.usable(ctx); err != nil {
		return nil, err
	}
	r, err := normalizeLiveFinalize(m.opts, in)
	if err != nil || r.Generation != m.opened.Generation {
		return nil, errors.Join(ErrManagedLiveBatchConflict, err)
	}
	return &ManagedLiveFinalizeAcquisition{owner: m, generation: r.Generation, requestHash: m.finalizeHash(r)}, nil
}

func (m *ManagedLive) requireFinalizeAcquisition(r ManagedLiveFinalize, a *ManagedLiveFinalizeAcquisition) error {
	if a == nil || a.owner != m || a.generation != r.Generation || a.generation != m.opened.Generation || a.requestHash != m.finalizeHash(r) {
		return ErrManagedLiveBatchConflict
	}
	return nil
}

func liveSafeHeads(c ManagedLiveControl, baseline map[uint64]ManagedLiveHead) map[uint64]ManagedLiveHead {
	if c.SafeOrdinal == 0 {
		return cloneLiveHeads(baseline)
	}
	return cloneLiveHeads(c.SafeHeads)
}

func livePublicFinality(c ManagedLiveControl, baseline map[uint64]ManagedLiveHead) map[uint64]ManagedLiveHead {
	if c.Finalizations == 0 {
		return cloneLiveHeads(baseline)
	}
	return cloneLiveHeads(c.Finality)
}

func liveActiveJournal(c ManagedLiveControl, active []ManagedLiveSegment, journal []managedLiveJournalEntry) error {
	if len(active) == 0 {
		if c.Tail != c.SafeOrdinal {
			return ErrManagedLiveBatchConflict
		}
	} else if active[0].Predecessor != c.SafeOrdinal || active[len(active)-1].Ordinal != c.Tail {
		return ErrManagedLiveBatchConflict
	}
	for _, segment := range active {
		if segment.Ordinal <= c.SafeOrdinal || segment.Ordinal >= c.Next {
			return ErrManagedLiveBatchConflict
		}
	}
	_, err := partitionLiveJournal(active, journal, c.SafeJournalBoundary)
	return err
}

func liveProofMap(proofs []ManagedLiveHead) (map[string]ManagedLiveHead, error) {
	out := map[string]ManagedLiveHead{}
	for _, head := range proofs {
		normalized, _, err := normalizeLiveHead(head)
		key := liveProofKey(head)
		if err != nil || normalized != head {
			return nil, ErrManagedLiveBatchConflict
		}
		if _, exists := out[key]; exists {
			return nil, ErrManagedLiveBatchConflict
		}
		out[key] = head
	}
	return out, nil
}

// Walk exact parent hashes through retained acquisitions, bounded by their
// count. A checkpoint comparison alone cannot confirm a competing fork.
func liveHeadLineage(head, baseline ManagedLiveHead, proofs map[string]ManagedLiveHead) (map[uint64]ManagedLiveHead, error) {
	_, base, be := normalizeLiveHead(baseline)
	_, cp, err := normalizeLiveHead(head)
	if err != nil || be != nil || cp.ChainID != base.ChainID || cp.BlockNumber < base.BlockNumber || proofs[liveProofKey(head)] != head {
		return nil, ErrManagedLiveBatchConflict
	}
	lineage := map[uint64]ManagedLiveHead{}
	for steps := 0; steps <= len(proofs); steps++ {
		lineage[cp.BlockNumber] = head
		if cp.BlockNumber == base.BlockNumber {
			if head != baseline {
				return nil, ErrManagedLiveBatchConflict
			}
			return lineage, nil
		}
		prior, exists := proofs[fmt.Sprintf("%d/%d/%s", cp.ChainID, cp.BlockNumber-1, head.ParentHash)]
		_, previous, err := normalizeLiveHead(prior)
		if !exists || err != nil || previous.BlockTimestamp > cp.BlockTimestamp {
			return nil, ErrManagedLiveBatchConflict
		}
		head, cp = prior, previous
	}
	return nil, ErrManagedLiveBatchConflict
}

// Used at finalize, subsequent batch admission and durable replay. Public
// finality may remain above a globally rewound private cursor, but both must
// describe the same branch. Heads above finality must also connect back to it.
func validateLiveFinalityBranch(opts ManagedLiveOptions, baseline, confirmed, safe, cursors map[uint64]ManagedLiveHead, active []ManagedLiveSegment, proofs []ManagedLiveHead) error {
	if len(baseline) != len(opts.Chains) || len(confirmed) != len(opts.Chains) || len(safe) != len(opts.Chains) || len(cursors) != len(opts.Chains) {
		return ErrManagedLiveBatchConflict
	}
	byKey, err := liveProofMap(proofs)
	if err != nil {
		return err
	}
	currentLines := map[uint64]map[uint64]ManagedLiveHead{}
	for _, chain := range opts.Chains {
		finalLine, err := liveHeadLineage(confirmed[chain.ID], baseline[chain.ID], byKey)
		if err != nil {
			return err
		}
		currentLine, err := liveHeadLineage(cursors[chain.ID], baseline[chain.ID], byKey)
		if err != nil {
			return err
		}
		_, finalCP, _ := normalizeLiveHead(confirmed[chain.ID])
		for number, head := range currentLine {
			if number <= finalCP.BlockNumber && finalLine[number] != head {
				return ErrManagedLiveBatchConflict
			}
		}
		_, safeCP, err := normalizeLiveHead(safe[chain.ID])
		if err != nil || safeCP.ChainID != chain.ID || currentLine[safeCP.BlockNumber] != safe[chain.ID] || finalLine[safeCP.BlockNumber] != safe[chain.ID] {
			return ErrManagedLiveBatchConflict
		}
		currentLines[chain.ID] = currentLine
	}
	for _, segment := range active {
		_, cp, err := normalizeLiveHead(segment.Block.Head)
		if err != nil || currentLines[cp.ChainID][cp.BlockNumber] != segment.Block.Head {
			return ErrManagedLiveBatchConflict
		}
	}
	return nil
}

func planLiveFinalize(opts ManagedLiveOptions, c ManagedLiveControl, baseline map[uint64]ManagedLiveHead, active []ManagedLiveSegment, journal []managedLiveJournalEntry, proofs []ManagedLiveHead, r ManagedLiveFinalize, execution ...map[string]ManagedLiveHead) (managedLiveFinalizePlan, error) {
	var plan managedLiveFinalizePlan
	normalized, err := normalizeLiveFinalize(opts, r)
	if err != nil || managedHash(normalized) != managedHash(r) {
		return plan, errors.Join(ErrManagedLiveBatchConflict, err)
	}
	if err := validateLiveControl(c); err != nil {
		return plan, err
	}
	if c.Batches == 0 || c.Finalizations == math.MaxUint64 || r.Generation != c.Generation || r.ExpectedLastOperation != c.LastOperation || r.ExpectedTail != c.Tail || r.ExpectedSafeOrdinal != c.SafeOrdinal {
		return plan, ErrManagedLiveBatchConflict
	}
	if err := liveActiveJournal(c, active, journal); err != nil {
		return plan, err
	}
	retained, err := liveProofMap(proofs)
	if err != nil {
		return plan, err
	}
	confirmed := livePublicFinality(c, baseline)
	logicalFinal := cloneLiveCheckpoints(c.LogicalFinality)
	beforeLogical := cloneLiveCheckpoints(c.LogicalFinality)
	before := cloneLiveHeads(confirmed)
	for id, head := range r.Finalized {
		_, cp, err := normalizeLiveHead(head)
		_, prior, pe := normalizeLiveHead(confirmed[id])
		if err != nil || pe != nil || cp.ChainID != id {
			return plan, ErrManagedLiveBatchConflict
		}
		if cp.BlockNumber < prior.BlockNumber || cp.BlockTimestamp < prior.BlockTimestamp {
			return plan, ErrManagedLiveFinalityRegression
		}
		if cp.BlockNumber == prior.BlockNumber && head != confirmed[id] {
			return plan, ErrManagedLiveBatchConflict
		}
		// Global undo can leave private/safe heads below both retained forks.
		// Require exact ancestry through the previous finalized identity too.
		// Durable receipt replay uses this same planner for every transition.
		if _, err := liveHeadLineage(head, confirmed[id], retained); err != nil {
			return plan, err
		}
		// Only validated batch segments prove execution; a bootstrap header is
		// insufficient. Keep this evidence across global undo, where a legitimate
		// confirmed target can stand above a private Down replay cursor.
		if opts.runtimeInitial && !positionReachedForOptions(logicalFinal[id], head.Checkpoint, opts) && (len(execution) != 1 || execution[0][liveProofKey(head)] != head) {
			return plan, ErrManagedLiveBatchConflict
		}
		confirmed[id] = head
		if opts.runtimeInitial {
			logicalFinal[id] = head.Checkpoint
			if opts.runtimeOmnichain {
				logicalFinal[id] = r.SourceCheckpoints[id]
			}
		}
	}
	safe := liveSafeHeads(c, baseline)
	if err := validateLiveFinalityBranch(opts, baseline, confirmed, safe, c.Cursors, active, proofs); err != nil {
		return plan, err
	}
	count := 0
	for _, segment := range active {
		_, cp, _ := normalizeLiveHead(segment.Block.Head)
		_, finalized, _ := normalizeLiveHead(confirmed[cp.ChainID])
		if cp.BlockNumber > finalized.BlockNumber || (opts.runtimeInitial && !positionReachedForOptions(logicalFinal[cp.ChainID], segment.Block.Head.Checkpoint, opts)) {
			break
		}
		count++ // Including no-DML blocks, in actual global execution order.
	}
	plan.After = c
	plan.After.Finalizations++
	plan.After.Finality = confirmed
	if opts.runtimeInitial {
		plan.After.LogicalFinality = logicalFinal
		plan.After.LogicalSafe = cloneLiveCheckpoints(c.LogicalSafe)
	}
	plan.After.LastOperation = r.ID
	plan.Pruned = append([]ManagedLiveSegment(nil), active[:count]...)
	if count > 0 {
		last := plan.Pruned[count-1]
		plan.After.SafeOrdinal = last.Ordinal
		plan.After.SafeJournalBoundary = last.Journal.After
		for _, segment := range plan.Pruned {
			_, cp, _ := normalizeLiveHead(segment.Block.Head)
			safe[cp.ChainID] = segment.Block.Head
			if opts.runtimeInitial {
				plan.After.LogicalSafe[cp.ChainID] = segment.Block.Head.Checkpoint
				if opts.runtimeOmnichain && segment.Block.SourceCheckpoint != "" {
					plan.After.LogicalSafe[cp.ChainID] = segment.Block.SourceCheckpoint
				}
			}
		}
		plan.After.SafeHeads = safe
		plan.After.SafeAnchors = map[uint64]string{}
		for id, head := range safe {
			plan.After.SafeAnchors[id] = head.Checkpoint
		}
		for _, row := range journal {
			if row.ID <= last.Journal.After {
				plan.Journal = append(plan.Journal, row)
			}
		}
		plan.Journal = copyLiveJournal(plan.Journal)
	}
	plan.Changed = count > 0 || managedHash(before) != managedHash(confirmed) || (opts.runtimeInitial && managedHash(beforeLogical) != managedHash(logicalFinal))
	return plan, nil
}

func makeLiveFinalizeEvidence(r ManagedLiveFinalize, hash string, before ManagedLiveControl, baseline map[uint64]ManagedLiveHead, plan managedLiveFinalizePlan) (managedLiveFinalizeEvidence, error) {
	e := managedLiveFinalizeEvidence{Version: managedLiveFinalizeVersion, Request: r, AcquisitionHash: hash, BeforeSafeHeads: liveSafeHeads(before, baseline), BeforeFinality: livePublicFinality(before, baseline), BeforeJournalBoundary: before.SafeJournalBoundary, Pruned: plan.Pruned, JournalJSON: []string{}, AfterControl: plan.After, Changed: plan.Changed}
	for _, row := range plan.Journal {
		raw, err := json.Marshal(row)
		if err != nil {
			return e, err
		}
		e.JournalJSON = append(e.JournalJSON, string(raw))
	}
	return e, nil
}

func exactLiveFinalizeRetry(r ManagedLiveFinalize, hash, owner string, op managedLiveOperation) (managedLiveFinalizeEvidence, error) {
	var e managedLiveFinalizeEvidence
	if op.Kind != "finalize" || op.ID != r.ID || op.Generation != r.Generation || op.OwnerToken != owner || op.RequestHash != hash || decodeLiveJSON(op.Payload, &e) != nil || e.Version != managedLiveFinalizeVersion || e.AcquisitionHash != hash || managedHash(e.Request) != managedHash(r) {
		return e, ErrManagedLiveBatchConflict
	}
	return e, nil
}

func decodeLiveFinalizeJournal(e managedLiveFinalizeEvidence) ([]managedLiveJournalEntry, error) {
	rows := make([]managedLiveJournalEntry, len(e.JournalJSON))
	for i, raw := range e.JournalJSON {
		if err := decodeLiveJSON([]byte(raw), &rows[i]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func liveFinalizeResult(e managedLiveFinalizeEvidence, applied bool) ManagedLiveFinalizeResult {
	safe := e.AfterControl.SafeHeads
	if e.AfterControl.SafeOrdinal == 0 {
		safe = e.BeforeSafeHeads
	}
	return ManagedLiveFinalizeResult{Applied: applied, Changed: e.Changed, OperationID: e.Request.ID, Pruned: append([]ManagedLiveSegment(nil), e.Pruned...), Finalized: cloneLiveHeads(e.AfterControl.Finality), Safe: cloneLiveHeads(safe), SafeOrdinal: e.AfterControl.SafeOrdinal}
}
