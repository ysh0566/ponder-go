package store

import (
	"encoding/json"
	"ponder.local/ponder-go/checkpoint"
	"strconv"
)

func runtimeRPCCacheEnabled(o ManagedLiveOptions, id uint64) bool {
	if o.runtimeCachePolicy == nil {
		return true
	}
	return o.runtimeCachePolicy[id]
}
func runtimeOptionsIdentityPayload(raw []byte, o ManagedLiveOptions) ([]byte, error) {
	base, err := runtimeIdentityPayload(raw, o.runtimeStarts)
	if err != nil {
		return nil, err
	}
	if o.runtimeInitial {
		return json.Marshal(struct {
			Runtime  json.RawMessage
			RPCCache map[uint64]bool
			Setup    bool
		}{base, o.runtimeCachePolicy, o.runtimeSetup})
	}
	if o.runtimeCachePolicy == nil {
		return base, err
	}
	return json.Marshal(struct {
		Runtime  json.RawMessage
		RPCCache map[uint64]bool
	}{base, o.runtimeCachePolicy})
}
func (d *ManagedLiveDomains) runtimePrunesAt(head ManagedLiveHead) bool {
	cp, err := checkpoint.Decode(head.Checkpoint)
	return err == nil && runtimeRPCCacheEnabled(d.live().opts, cp.ChainID)
}
func runtimeCacheDeletedAllowed(o ManagedLiveOptions, rows []string) bool {
	if o.runtimeCachePolicy == nil {
		return true
	}
	for _, raw := range rows {
		var row map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &row) != nil {
			return false
		}
		id, err := strconv.ParseUint(string(row["chain_id"]), 10, 64)
		if err != nil || !runtimeRPCCacheEnabled(o, id) {
			return false
		}
	}
	return true
}

func runtimeDeletedRow(raw string) (uint64, uint64, bool) {
	var row map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &row) != nil {
		return 0, 0, false
	}
	id, e := strconv.ParseUint(string(row["chain_id"]), 10, 64)
	n, e2 := strconv.ParseUint(string(row["block_number"]), 10, 64)
	return id, n, e == nil && e2 == nil
}
func runtimeReorgDeletedAllowed(o ManagedLiveOptions, in ManagedLiveDomainReorg, rows []string) bool {
	if o.runtimeCachePolicy == nil {
		return true
	}
	ancestor, err := checkpoint.Decode(in.Reorg.Ancestor.Checkpoint)
	if err != nil {
		return false
	}
	allowed := map[uint64]bool{}
	for _, h := range in.Orphaned {
		cp, e := checkpoint.Decode(h.Checkpoint)
		if e != nil || cp.ChainID != ancestor.ChainID {
			return false
		}
		allowed[cp.BlockNumber] = true
	}
	for _, raw := range rows {
		id, n, ok := runtimeDeletedRow(raw)
		if !ok || id != ancestor.ChainID || !runtimeRPCCacheEnabled(o, id) || !allowed[n] {
			return false
		}
	}
	return true
}
func runtimeFenceDeletedAllowed(o ManagedLiveOptions, cuts map[uint64]uint64, rows []string) bool {
	if o.runtimeCachePolicy == nil {
		return true
	}
	for _, raw := range rows {
		id, n, ok := runtimeDeletedRow(raw)
		cut, known := cuts[id]
		if !ok || !known || !runtimeRPCCacheEnabled(o, id) || n <= cut {
			return false
		}
	}
	return true
}
