package store

import (
	"context"
	"encoding/json"
	"fmt"
	"ponder.local/ponder-go/checkpoint"
	"strings"
)

type omnichainPendingBatchKey struct{}

// Only the native writer may audit its not-yet-committed intermediate batch.
// Public reads and commit reconciliation always require the paired safe receipt.
func validateOmnichainBatchSafes(d managedLiveDurable, pending string) error {
	byID := map[string]managedLiveOperation{}
	for _, op := range d.Operations {
		byID[op.ID] = op
	}
	for _, op := range d.Operations {
		if op.Kind != "batch" {
			continue
		}
		var b managedLiveBatchEvidence
		if err := decodeLiveJSON(op.Payload, &b); err != nil {
			return err
		}
		if len(b.Batch.Blocks) == 0 {
			continue
		}
		safe, ok := byID[omnichainSafeID(op.ID)]
		if !ok {
			if pending == op.ID && d.Control.LastOperation == op.ID {
				continue
			}
			return fmt.Errorf("omnichain batch has no atomic safe receipt")
		}
		var e managedLiveFinalizeEvidence
		if err := decodeLiveJSON(safe.Payload, &e); err != nil {
			return err
		}
		heads := map[uint64]ManagedLiveHead{}
		positions := map[uint64]string{}
		for _, block := range b.Batch.Blocks {
			cp, _ := checkpoint.Decode(block.Head.Checkpoint)
			heads[cp.ChainID] = block.Head
			positions[cp.ChainID] = block.Head.Checkpoint
			if block.SourceCheckpoint != "" {
				positions[cp.ChainID] = block.SourceCheckpoint
			}
		}
		if safe.Kind != "finalize" || safe.PreviousOperation != op.ID || safe.OwnerToken != op.OwnerToken || safe.Generation != op.Generation || e.Request.ID != safe.ID || e.Request.ExpectedLastOperation != op.ID || e.Request.Generation != op.Generation || managedHash(e.Request.Finalized) != managedHash(heads) || managedHash(e.Request.SourceCheckpoints) != managedHash(positions) {
			return fmt.Errorf("omnichain atomic safe receipt differs from exact batch")
		}
	}
	return nil
}

// Optional v11 wire fields are not silently accepted by an older receipt format,
// including explicit empty strings/maps/null. Only receipt structure is scanned;
// journal row JSON remains an opaque string bound by its existing digest.
func validateLegacyLiveExtensions(ops []managedLiveOperation) error {
	var forbidden func(any) bool
	forbidden = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if strings.EqualFold(k, "sourceCheckpoint") || strings.EqualFold(k, "sourceCheckpoints") {
					return true
				}
				if forbidden(v) {
					return true
				}
			}
		case []any:
			for _, v := range x {
				if forbidden(v) {
					return true
				}
			}
		}
		return false
	}
	for _, op := range ops {
		var raw any
		if err := json.Unmarshal([]byte(op.Payload), &raw); err != nil {
			return err
		}
		if forbidden(raw) {
			return fmt.Errorf("legacy live receipt contains omnichain extension fields")
		}
	}
	return nil
}
func omnichainPendingContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, omnichainPendingBatchKey{}, id)
}
