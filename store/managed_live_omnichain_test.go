package store

import (
	"encoding/json"
	"ponder.local/ponder-go/checkpoint"
	"testing"
)

func TestOmnichainAtomicSafeReceiptBinding(t *testing.T) {
	head := ManagedLiveHead{Checkpoint: checkpoint.MustEncode(checkpoint.Block(100, 1, 1, true)), Hash: "0x" + "1111111111111111111111111111111111111111111111111111111111111111", ParentHash: "0x" + "0000000000000000000000000000000000000000000000000000000000000000"}
	event := checkpoint.Block(100, 1, 1, true)
	event.EventType = checkpoint.Blocks
	event.EventIndex = 0
	position := checkpoint.MustEncode(event)
	b := ManagedLiveBatch{ID: "batch", Generation: 1, Blocks: []ManagedLiveBlock{{Head: head, InputDigest: "digest", SourceCheckpoint: position}}}
	raw, _ := json.Marshal(managedLiveBatchEvidence{Version: managedLiveBatchVersion, Batch: b})
	batch := managedLiveOperation{ID: b.ID, Kind: "batch", Generation: 1, OwnerToken: "owner", Payload: json.RawMessage(raw)}
	request := ManagedLiveFinalize{ID: omnichainSafeID(b.ID), Generation: 1, ExpectedLastOperation: b.ID, Finalized: map[uint64]ManagedLiveHead{1: head}, SourceCheckpoints: map[uint64]string{1: position}}
	raw, _ = json.Marshal(managedLiveFinalizeEvidence{Version: managedLiveFinalizeVersion, Request: request})
	safe := managedLiveOperation{ID: request.ID, Kind: "finalize", Generation: 1, OwnerToken: "owner", PreviousOperation: b.ID, Payload: json.RawMessage(raw)}
	for _, name := range []string{"valid", "missing", "wrong-predecessor", "wrong-owner", "wrong-generation", "wrong-source-checkpoint", "pending-public", "pending-writer"} {
		t.Run(name, func(t *testing.T) {
			d := managedLiveDurable{Control: ManagedLiveControl{LastOperation: safe.ID}, Operations: []managedLiveOperation{batch, safe}}
			pending := ""
			switch name {
			case "missing":
				d.Operations = d.Operations[:1]
			case "wrong-predecessor":
				d.Operations[1].PreviousOperation = "different"
			case "wrong-owner":
				d.Operations[1].OwnerToken = "different"
			case "wrong-generation":
				d.Operations[1].Generation = 2
			case "wrong-source-checkpoint":
				r := request
				r.SourceCheckpoints = map[uint64]string{1: head.Checkpoint}
				v, _ := json.Marshal(managedLiveFinalizeEvidence{Version: managedLiveFinalizeVersion, Request: r})
				d.Operations[1].Payload = v
			case "pending-public", "pending-writer":
				d.Operations = d.Operations[:1]
				d.Control.LastOperation = batch.ID
				if name == "pending-writer" {
					pending = batch.ID
				}
			}
			err := validateOmnichainBatchSafes(d, pending)
			wantOK := name == "valid" || name == "pending-writer"
			if (err == nil) != wantOK {
				t.Fatalf("binding%s err%v", name, err)
			}
		})
	}
}
func TestLegacyLiveRejectsOmnichainWireFields(t *testing.T) {
	for _, fragment := range []string{`{"SourceCheckpoint":""}`, `{"SOURCECHECKPOINTS":null}`, `{"sourceCheckpoint":""}`, `{"sourceCheckpoint":null}`, `{"sourceCheckpoint":"value"}`, `{"sourceCheckpoints":{}}`, `{"sourceCheckpoints":null}`, `{"sourceCheckpoints":{"1":"value"}}`} {
		t.Run(fragment, func(t *testing.T) {
			raw := `{"batch":{"blocks":[` + fragment + `]}}`
			for _, kind := range []string{"batch", "finalize", "recover", "reorg"} {
				if err := validateLegacyLiveExtensions([]managedLiveOperation{{Kind: kind, Payload: json.RawMessage(raw)}}); err == nil {
					t.Fatalf("legacy %s extension accepted", kind)
				}
			}
		})
	}
	if err := validateLegacyLiveExtensions([]managedLiveOperation{{Kind: "batch", Payload: json.RawMessage(`{"batch":{"blocks":[{"head":{}}]}}`)}}); err != nil {
		t.Fatal(err)
	}
}
