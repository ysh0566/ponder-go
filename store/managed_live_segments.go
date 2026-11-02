package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"ponder.local/ponder-go/checkpoint"
)

const managedLiveBatchVersion = "managed-live-batch-v1"
const managedLiveMaxBatchBlocks = 1024

var (
	ErrManagedLiveBatchConflict = errors.New("managed live batch does not match its exact inputs or predecessor")
	ErrManagedLiveBusy          = errors.New("managed live batch is busy")
	ErrManagedLiveUncertain     = errors.New("managed live outcome is unknown; close this owner before recovery")
)

// ManagedLiveBlock describes one already-acquired block, including blocks with
// no events or DML. InputDigest is the SHA256 of the caller's extracted inputs;
// BuildID remains the identity of the handler implementation.
type ManagedLiveBlock struct {
	Head        ManagedLiveHead `json:"head"`
	InputDigest string          `json:"inputDigest"`
}

// ManagedLiveBatch follows actual execution order, never checkpoint sort order.
// The first batch supplies an explicit acquired baseline for every configured
// chain. This is an explicit already-processed boundary; historical start/setup
// selection is not implemented here. Later batches must omit Baselines.
type ManagedLiveBatch struct {
	ID                    string                     `json:"id"`
	Generation            uint64                     `json:"generation"`
	ExpectedLastOperation string                     `json:"expectedLastOperation"`
	ExpectedTail          uint64                     `json:"expectedTail"`
	Baselines             map[uint64]ManagedLiveHead `json:"baselines,omitempty"`
	InitialCheckpoints    map[uint64]string          `json:"initialCheckpoints,omitempty"`
	Blocks                []ManagedLiveBlock         `json:"blocks"`
}

// ManagedLiveAcquisition retains a private copy of the caller's acquired data.
// It does not fetch RPC data or claim to authenticate an RPC response. The
// constructor-owned generation and exact header/input snapshot cannot be
// retargeted by later mutation of the caller's batch.
type ManagedLiveAcquisition struct {
	owner       *ManagedLive
	generation  uint64
	requestHash string
}

type ManagedLiveJournalSpan struct {
	Before int64  `json:"before"`
	After  int64  `json:"after"`
	Count  uint64 `json:"count"`
	Digest string `json:"digest"`
}

type ManagedLiveSegment struct {
	Ordinal          uint64                 `json:"ordinal"`
	Predecessor      uint64                 `json:"predecessor"`
	OperationID      string                 `json:"operationId"`
	Generation       uint64                 `json:"generation"`
	Block            ManagedLiveBlock       `json:"block"`
	BeforeCursor     ManagedLiveHead        `json:"beforeCursor"`
	BeforeCheckpoint string                 `json:"beforeCheckpoint,omitempty"`
	Journal          ManagedLiveJournalSpan `json:"journal"`
}

type ManagedLiveBatchResult struct {
	Applied     bool                 `json:"applied"`
	OperationID string               `json:"operationId"`
	Segments    []ManagedLiveSegment `json:"segments"`
}

// Evidence is copied into the independent operations table. No foreign key or
// cascade ties it to segment retention; reorg archives retain removed evidence.
type managedLiveBatchEvidence struct {
	Version         string                     `json:"version"`
	Batch           ManagedLiveBatch           `json:"batch"`
	AcquisitionHash string                     `json:"acquisitionHash"`
	BeforeCursors   map[uint64]ManagedLiveHead `json:"beforeCursors"`
	Segments        []ManagedLiveSegment       `json:"segments"`
	AfterControl    ManagedLiveControl         `json:"afterControl"`
}

type managedLiveOperation struct {
	ID, OwnerToken, Kind, RequestHash, PreviousOperation, BeforeHash, AfterHash string
	Generation                                                                  uint64
	Payload                                                                     json.RawMessage
}

type managedLiveJournalEntry struct {
	ID         int64           `json:"id"`
	Table      string          `json:"table"`
	Key        json.RawMessage `json:"key"`
	Before     json.RawMessage `json:"before"`
	Row        json.RawMessage `json:"row"`
	Operation  int             `json:"operation"`
	Checkpoint string          `json:"checkpoint"`
	ChainID    uint64          `json:"chainId"`
}

func liveID(id string) bool {
	return id != "" && len(id) <= 128 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\x00\r\n")
}

func liveDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func normalizeLiveHead(head ManagedLiveHead) (ManagedLiveHead, checkpoint.Checkpoint, error) {
	cp, err := checkpoint.Decode(head.Checkpoint)
	if err != nil || cp.ChainID > 9007199254740991 || cp.BlockNumber > 9007199254740991 || cp == checkpoint.Max || cp != checkpoint.Block(cp.BlockTimestamp, cp.ChainID, cp.BlockNumber, true) {
		return head, cp, fmt.Errorf("managed live requires a canonical acquired block/end checkpoint")
	}
	for _, value := range []string{head.Hash, head.ParentHash} {
		if len(value) != 66 || !strings.HasPrefix(value, "0x") {
			return head, cp, fmt.Errorf("managed live requires full acquired block hashes")
		}
		if _, err := hex.DecodeString(value[2:]); err != nil {
			return head, cp, err
		}
	}
	head.Hash, head.ParentHash = strings.ToLower(head.Hash), strings.ToLower(head.ParentHash)
	return head, cp, nil
}

func cloneLiveHeads(in map[uint64]ManagedLiveHead) map[uint64]ManagedLiveHead {
	out := make(map[uint64]ManagedLiveHead, len(in))
	for id, head := range in {
		out[id] = head
	}
	return out
}

func normalizeLiveBatch(opts ManagedLiveOptions, in ManagedLiveBatch) (ManagedLiveBatch, error) {
	if !liveID(in.ID) || !liveID(in.ExpectedLastOperation) || in.ID == in.ExpectedLastOperation || in.Generation == 0 || in.Generation >= math.MaxInt64 || in.ExpectedTail >= math.MaxInt64-1 || (len(in.Blocks) == 0 && !(opts.liveRuntime && len(in.Baselines) > 0)) || len(in.Blocks) > managedLiveMaxBatchBlocks {
		return in, ErrManagedLiveBatchConflict
	}
	chains := map[uint64]bool{}
	for _, chain := range opts.Chains {
		chains[chain.ID] = true
	}
	out := in
	if !opts.runtimeInitial && len(in.InitialCheckpoints) > 0 {
		return out, ErrManagedLiveBatchConflict
	}
	out.InitialCheckpoints = cloneLiveCheckpoints(in.InitialCheckpoints)
	out.Baselines = nil
	if len(in.Baselines) > 0 {
		out.Baselines = map[uint64]ManagedLiveHead{}
	}
	out.Blocks = append([]ManagedLiveBlock(nil), in.Blocks...)
	for id, head := range in.Baselines {
		head, cp, err := normalizeLiveHead(head)
		if err != nil || cp.ChainID != id || !chains[id] {
			return out, ErrManagedLiveBatchConflict
		}
		out.Baselines[id] = head
	}
	for i, block := range out.Blocks {
		head, cp, err := normalizeLiveHead(block.Head)
		block.InputDigest = strings.ToLower(block.InputDigest)
		if err != nil || !chains[cp.ChainID] || !liveDigest(block.InputDigest) {
			return out, ErrManagedLiveBatchConflict
		}
		block.Head = head
		out.Blocks[i] = block
	}
	return out, nil
}

func (m *ManagedLive) usable(ctx context.Context) error {
	if m.owner.closed.Load() {
		return ErrManagedLiveClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.owner.lost.Load() {
		return ErrLeaseLost
	}
	if m.uncertain.Load() {
		return ErrManagedLiveUncertain
	}
	return nil
}

func (m *ManagedLive) batchHash(request ManagedLiveBatch) string {
	return liveBatchHash(m.owner.identity, m.owner.s.leaseToken, request)
}

func liveBatchHash(identity, owner string, request ManagedLiveBatch) string {
	return managedHash(struct {
		Identity, Owner string
		Batch           ManagedLiveBatch
	}{identity, owner, request})
}

func (m *ManagedLive) RetainBatch(ctx context.Context, batch ManagedLiveBatch) (*ManagedLiveAcquisition, error) {
	if err := m.usable(ctx); err != nil {
		return nil, err
	}
	normalized, err := normalizeLiveBatch(m.opts, batch)
	if err != nil || normalized.Generation != m.opened.Generation {
		return nil, errors.Join(ErrManagedLiveBatchConflict, err)
	}
	return &ManagedLiveAcquisition{owner: m, generation: normalized.Generation, requestHash: m.batchHash(normalized)}, nil
}

func (m *ManagedLive) requireAcquisition(batch ManagedLiveBatch, acquired *ManagedLiveAcquisition) error {
	if acquired == nil || acquired.owner != m || acquired.generation != batch.Generation || acquired.generation != m.opened.Generation || acquired.requestHash != m.batchHash(batch) {
		return ErrManagedLiveBatchConflict
	}
	return nil
}

func validateLiveControl(c ManagedLiveControl) error {
	if err := validateLivePositions(c); err != nil {
		return err
	}
	if c.Bootstraps != 0 && (!isLiveRuntimeVersion(c.Version) || c.Bootstraps != 1 || c.Bootstraps > c.Batches) {
		return ErrManagedLiveBatchConflict
	}
	if (c.Serving || c.Activations != 0) && (!isLiveRuntimeVersion(c.Version) || c.Serving && c.Activations == 0) {
		return ErrManagedLiveBatchConflict
	}
	if (c.Version != managedLiveVersion && c.Version != managedLiveRecoveryVersion && c.Version != managedLiveDomainsVersion && !isLiveRuntimeVersion(c.Version)) || c.Generation == 0 || c.Generation >= math.MaxInt64 || c.Phase != "history" || c.Next == 0 || c.Next >= math.MaxInt64 || c.Tail >= c.Next || c.SafeOrdinal > c.Tail || c.SafeJournalBoundary < 0 || !liveID(c.LastOperation) || !liveDigest(c.CatalogHash) || (c.SafeOrdinal == 0 && (len(c.SafeAnchors) != 0 || len(c.SafeHeads) != 0 || c.SafeJournalBoundary != 0)) || (c.SafeOrdinal > 0 && (len(c.SafeAnchors) == 0 || len(c.SafeHeads) != len(c.SafeAnchors) || c.Finalizations == 0)) {
		return ErrManagedLivePendingActions
	}
	if (c.Finalizations == 0 && len(c.Finality) != 0) || (c.Finalizations > 0 && (len(c.Finality) == 0 || c.Batches == 0)) {
		return ErrManagedLivePendingActions
	}
	for id, head := range c.SafeHeads {
		normalized, cp, err := normalizeLiveHead(head)
		if err != nil || normalized != head || cp.ChainID != id || c.SafeAnchors[id] != head.Checkpoint {
			return ErrManagedLivePendingActions
		}
	}
	if (c.Version == managedLiveVersion && (c.Recoveries != 0 || len(c.ReplayTargets) != 0)) || (c.Recoveries == 0 && len(c.ReplayTargets) != 0 && !(isContinuousRuntimeVersion(c.Version) && c.Reorgs > 0)) {
		return ErrManagedLivePendingActions
	}
	if (c.Reorgs == 0 && c.Recoveries == 0 && (c.Next != c.Tail+1 || c.Batches-c.Bootstraps > c.Tail)) || (c.Reorgs > 0 && c.Batches == 0) || (c.Batches == 0 && (c.Tail != 0 || len(c.Cursors) != 0)) || (c.Batches > 0 && len(c.Cursors) == 0) {
		return ErrManagedLivePendingActions
	}
	return nil
}

func planLiveBatch(opts ManagedLiveOptions, c ManagedLiveControl, batch ManagedLiveBatch) ([]ManagedLiveSegment, map[uint64]ManagedLiveHead, ManagedLiveControl, error) {
	if err := validateLiveControl(c); err != nil {
		return nil, nil, c, err
	}
	if batch.Generation != c.Generation || batch.ExpectedLastOperation != c.LastOperation || batch.ExpectedTail != c.Tail || c.Batches == math.MaxUint64 || (len(batch.Blocks) == 0 && !(opts.liveRuntime && c.Batches == 0)) || uint64(len(batch.Blocks)) >= uint64(math.MaxInt64)-c.Next {
		return nil, nil, c, ErrManagedLiveBatchConflict
	}
	if (opts.runtimeSetup || opts.runtimeInitial) && c.Batches == 0 && len(batch.Blocks) != 0 {
		return nil, nil, c, ErrManagedLiveBatchConflict
	}
	cursors := cloneLiveHeads(c.Cursors)
	positions := cloneLiveCheckpoints(c.LogicalCursors)
	if opts.runtimeInitial && positions == nil {
		positions = map[uint64]string{}
	}
	if c.Batches == 0 {
		if len(batch.Baselines) != len(opts.Chains) {
			return nil, nil, c, fmt.Errorf("first managed batch requires every explicit acquired baseline")
		}
		for _, chain := range opts.Chains {
			head, ok := batch.Baselines[chain.ID]
			_, cp, err := normalizeLiveHead(head)
			fresh, fe := checkpoint.Decode(opts.FinalizedHeads[chain.ID].Checkpoint)
			if !ok || err != nil || fe != nil || cp.ChainID != chain.ID || cp.BlockNumber > min(fresh.BlockNumber, opts.EndBlocks[chain.ID]) || cp.BlockTimestamp > fresh.BlockTimestamp || (cp.BlockNumber == fresh.BlockNumber && head != opts.FinalizedHeads[chain.ID]) {
				return nil, nil, c, fmt.Errorf("invalid explicit managed baseline for chain %d", chain.ID)
			}
			if opts.runtimeInitial {
				expectedNumber := min(opts.runtimeStarts[chain.ID], fresh.BlockNumber)
				expected := head.Checkpoint
				if opts.runtimeStarts[chain.ID] <= fresh.BlockNumber {
					expected = checkpoint.MustEncode(checkpoint.Block(cp.BlockTimestamp, chain.ID, cp.BlockNumber, false))
				}
				if cp.BlockNumber != expectedNumber || len(batch.InitialCheckpoints) != len(opts.Chains) || batch.InitialCheckpoints[chain.ID] != expected {
					return nil, nil, c, ErrManagedLiveBatchConflict
				}
				positions[chain.ID] = expected
			} else if opts.liveRuntime && (opts.runtimeStarts[chain.ID] == 0 || cp.BlockNumber != min(opts.runtimeStarts[chain.ID]-1, fresh.BlockNumber, opts.EndBlocks[chain.ID])) {
				return nil, nil, c, ErrManagedLiveBatchConflict
			}
			cursors[chain.ID] = head
		}
	} else if len(batch.Baselines) != 0 || len(batch.InitialCheckpoints) != 0 || len(cursors) != len(opts.Chains) {
		return nil, nil, c, ErrManagedLiveBatchConflict
	}
	before := cloneLiveHeads(cursors)
	segments := make([]ManagedLiveSegment, 0, len(batch.Blocks))
	predecessor := c.Tail
	for i, block := range batch.Blocks {
		_, cp, err := normalizeLiveHead(block.Head)
		prior, ok := cursors[cp.ChainID]
		previous, pe := checkpoint.Decode(prior.Checkpoint)
		beforePosition := prior.Checkpoint
		if opts.runtimeInitial {
			beforePosition = positions[cp.ChainID]
		}
		extends := cp.BlockNumber == previous.BlockNumber+1 && block.Head.ParentHash == prior.Hash
		if opts.runtimeInitial && liveDown(beforePosition) {
			extends = cp.BlockNumber == previous.BlockNumber && block.Head == prior
		}
		if err != nil || pe != nil || !ok || !extends || cp.BlockTimestamp < previous.BlockTimestamp || cp.BlockNumber > opts.EndBlocks[cp.ChainID] {
			return nil, nil, c, fmt.Errorf("managed block must extend its exact acquired cursor without gaps")
		}
		ordinal := c.Next + uint64(i)
		segments = append(segments, ManagedLiveSegment{Ordinal: ordinal, Predecessor: predecessor, OperationID: batch.ID, Generation: c.Generation, Block: block, BeforeCursor: prior})
		if opts.runtimeInitial {
			segments[len(segments)-1].BeforeCheckpoint = beforePosition
			positions[cp.ChainID] = block.Head.Checkpoint
		}
		predecessor = ordinal
		cursors[cp.ChainID] = block.Head
	}
	next := c
	next.Next += uint64(len(segments))
	next.Tail = next.Next - 1
	next.Batches++
	if opts.liveRuntime && len(batch.Blocks) == 0 {
		next.Bootstraps++
	}
	next.Cursors = cursors
	if opts.runtimeInitial {
		next.LogicalCursors = positions
		if c.Batches == 0 {
			next.LogicalSafe = cloneLiveCheckpoints(positions)
			next.LogicalFinality = cloneLiveCheckpoints(positions)
		}
	}
	next.LastOperation = batch.ID
	if len(next.ReplayTargets) != 0 && liveControlReplayComplete(next, next.ReplayTargets) {
		next.ReplayTargets = nil
	}
	return segments, before, next, nil
}

func liveJournalSpan(before int64, head ManagedLiveHead, rows []managedLiveJournalEntry) (ManagedLiveJournalSpan, error) {
	cp, err := checkpoint.Decode(head.Checkpoint)
	if err != nil || before < 0 {
		return ManagedLiveJournalSpan{}, ErrManagedLiveBatchConflict
	}
	after := before
	for _, row := range rows {
		if row.ID <= after || row.Table == "" || row.ChainID != cp.ChainID || row.Checkpoint != head.Checkpoint || row.Operation < 0 || row.Operation > 2 || !json.Valid(row.Key) || !json.Valid(row.Before) || !json.Valid(row.Row) {
			return ManagedLiveJournalSpan{}, fmt.Errorf("managed journal attribution or numeric operation order differs")
		}
		after = row.ID
	}
	// Use the same nonnil empty array for empty blocks and no-op handlers.
	if rows == nil {
		rows = []managedLiveJournalEntry{}
	}
	return ManagedLiveJournalSpan{Before: before, After: after, Count: uint64(len(rows)), Digest: managedHash(rows)}, nil
}

func decodeLiveJSON(raw []byte, target any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("managed payload has trailing data: %v", err)
	}
	return nil
}

func orderLiveOperations(ops []managedLiveOperation, terminal string) ([]managedLiveOperation, error) {
	byID := map[string]managedLiveOperation{}
	for _, op := range ops {
		if !liveID(op.ID) || op.Generation == 0 || op.Generation >= math.MaxInt64 || op.OwnerToken == "" || !liveDigest(op.RequestHash) || !liveDigest(op.AfterHash) {
			return nil, ErrManagedLiveBatchConflict
		}
		if _, exists := byID[op.ID]; exists {
			return nil, ErrManagedLiveBatchConflict
		}
		byID[op.ID] = op
	}
	reverse := []managedLiveOperation{}
	seen := map[string]bool{}
	for id := terminal; id != ""; {
		op, exists := byID[id]
		if !exists || seen[id] {
			return nil, fmt.Errorf("managed operation predecessor is missing or cyclic")
		}
		seen[id] = true
		reverse = append(reverse, op)
		id = op.PreviousOperation
	}
	if len(reverse) == 0 || len(reverse) != len(ops) {
		return nil, fmt.Errorf("managed operation chain has unbound receipts")
	}
	ordered := make([]managedLiveOperation, len(reverse))
	for i := range reverse {
		op := reverse[len(reverse)-1-i]
		before := ""
		if i > 0 {
			before = ordered[i-1].AfterHash
		}
		if op.BeforeHash != before {
			return nil, fmt.Errorf("managed operation before/after proof chain differs")
		}
		ordered[i] = op
	}
	return ordered, nil
}

func exactLiveRetry(batch ManagedLiveBatch, requestHash, owner string, op managedLiveOperation) (managedLiveBatchEvidence, error) {
	var evidence managedLiveBatchEvidence
	if op.Kind != "batch" || op.ID != batch.ID || op.Generation != batch.Generation || op.OwnerToken != owner || op.RequestHash != requestHash || decodeLiveJSON(op.Payload, &evidence) != nil || evidence.Version != managedLiveBatchVersion || evidence.AcquisitionHash != requestHash || managedHash(evidence.Batch) != managedHash(batch) {
		return evidence, ErrManagedLiveBatchConflict
	}
	return evidence, nil
}
