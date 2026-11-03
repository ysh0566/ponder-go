package store

import (
	"fmt"
	"math"
	"ponder.local/ponder-go/checkpoint"
)

const managedLiveSourceVersion = "managed-live-source-v9"

func cloneLiveCheckpoints(in map[uint64]string) map[uint64]string {
	if in == nil {
		return nil
	}
	out := make(map[uint64]string, len(in))
	for id, cp := range in {
		out[id] = cp
	}
	return out
}
func liveSourceVersion(c ManagedLiveControl) bool {
	return c.Version == managedLiveSourceVersion || c.Version == managedLiveFactoryVersion || c.Version == managedLiveOmnichainVersion || c.Version == managedLiveOmnichainLogOnlyVersion
}
func liveCanonicalPosition(raw string, omni ...bool) (checkpoint.Checkpoint, bool) {
	cp, e := checkpoint.Decode(raw)
	event := checkpoint.Block(cp.BlockTimestamp, cp.ChainID, cp.BlockNumber, true)
	event.EventType = checkpoint.Blocks
	event.EventIndex = 0
	return cp, e == nil && cp != checkpoint.Max && (cp == checkpoint.Block(cp.BlockTimestamp, cp.ChainID, cp.BlockNumber, false) || cp == checkpoint.Block(cp.BlockTimestamp, cp.ChainID, cp.BlockNumber, true) || (len(omni) == 1 && omni[0] && cp == event))
}
func liveDown(raw string) bool {
	cp, ok := liveCanonicalPosition(raw)
	return ok && cp == checkpoint.Block(cp.BlockTimestamp, cp.ChainID, cp.BlockNumber, false)
}
func livePositionForHead(raw string, head ManagedLiveHead, omni ...bool) bool {
	cp, ok := liveCanonicalPosition(raw, omni...)
	_, h, e := normalizeLiveHead(head)
	return ok && e == nil && cp.BlockTimestamp == h.BlockTimestamp && cp.ChainID == h.ChainID && cp.BlockNumber == h.BlockNumber
}
func liveCursorCheckpoint(c ManagedLiveControl, id uint64) string {
	if liveSourceVersion(c) {
		return c.LogicalCursors[id]
	}
	return c.Cursors[id].Checkpoint
}
func liveSafeCheckpoint(c ManagedLiveControl, base map[uint64]ManagedLiveHead, id uint64) string {
	if liveSourceVersion(c) {
		return c.LogicalSafe[id]
	}
	return liveSafeHeads(c, base)[id].Checkpoint
}
func liveFinalCheckpoint(c ManagedLiveControl, base map[uint64]ManagedLiveHead, id uint64) string {
	if liveSourceVersion(c) {
		return c.LogicalFinality[id]
	}
	return livePublicFinality(c, base)[id].Checkpoint
}

// Positions order execution boundaries by height and edge, never fork timestamp.
func livePositionReached(actual, target string, omni ...bool) bool {
	a, ok := liveCanonicalPosition(actual, omni...)
	b, bok := liveCanonicalPosition(target, omni...)
	return ok && bok && a.ChainID == b.ChainID && (a.BlockNumber > b.BlockNumber || (a.BlockNumber == b.BlockNumber && (!liveDown(actual) || liveDown(target))))
}
func liveControlReplayComplete(c ManagedLiveControl, targets map[uint64]string) bool {
	if !liveSourceVersion(c) {
		return liveReplayComplete(c.Cursors, targets)
	}
	for id, t := range targets {
		if !positionReachedForControl(liveCursorCheckpoint(c, id), t, c) {
			return false
		}
	}
	return true
}

// ManagedLiveCursorCheckpoint is the logical processing position. Header proofs remain Up.
func ManagedLiveCursorCheckpoint(c ManagedLiveControl, id uint64) string {
	return liveCursorCheckpoint(c, id)
}

// ManagedLiveNextBlock preserves the inclusive first block at a Down boundary.
func ManagedLiveNextBlock(c ManagedLiveControl, id uint64) (uint64, error) {
	cp, ok := controlPosition(liveCursorCheckpoint(c, id), c)
	if !ok || cp.ChainID != id {
		return 0, ErrManagedLiveBatchConflict
	}
	if liveDown(liveCursorCheckpoint(c, id)) {
		return cp.BlockNumber, nil
	}
	if cp.BlockNumber == math.MaxUint64 {
		return 0, fmt.Errorf("runtime block number exhausted")
	}
	return cp.BlockNumber + 1, nil
}
func ManagedLiveReachedBlock(c ManagedLiveControl, id, n uint64) bool {
	cp, ok := controlPosition(liveCursorCheckpoint(c, id), c)
	return ok && (cp.BlockNumber > n || (cp.BlockNumber == n && !liveDown(liveCursorCheckpoint(c, id))))
}
func validateLivePositions(c ManagedLiveControl) error {
	if !liveSourceVersion(c) {
		if len(c.LogicalCursors)+len(c.LogicalSafe)+len(c.LogicalFinality) != 0 {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	if c.Batches == 0 {
		if len(c.LogicalCursors)+len(c.LogicalSafe)+len(c.LogicalFinality) != 0 {
			return ErrManagedLiveBatchConflict
		}
		return nil
	}
	if len(c.LogicalCursors) != len(c.Cursors) || len(c.LogicalSafe) != len(c.Cursors) || len(c.LogicalFinality) != len(c.Cursors) {
		return ErrManagedLiveBatchConflict
	}
	for id, head := range c.Cursors {
		if !positionForControl(c.LogicalCursors[id], head, c) {
			return ErrManagedLiveBatchConflict
		}
		for _, p := range []string{c.LogicalSafe[id], c.LogicalFinality[id]} {
			cp, ok := controlPosition(p, c)
			if !ok || cp.ChainID != id {
				return ErrManagedLiveBatchConflict
			}
		}
	}
	return nil
}

// liveExecutedHeads is called only after the enclosing durable receipt set has
// been audited. Baselines and mere acquired proofs deliberately grant nothing.
func liveExecutedHeads(ops []managedLiveOperation) map[string]ManagedLiveHead {
	out := map[string]ManagedLiveHead{}
	for _, op := range ops {
		if op.Kind != "batch" {
			continue
		}
		var e managedLiveBatchEvidence
		if decodeLiveJSON(op.Payload, &e) != nil || e.Version != managedLiveBatchVersion {
			return nil
		}
		for _, s := range e.Segments {
			out[liveProofKey(s.Block.Head)] = s.Block.Head
		}
	}
	return out
}
