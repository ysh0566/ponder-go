package store

import "encoding/json"

func runtimeIdentityPayload(base []byte, starts map[uint64]uint64) ([]byte, error) {
	return json.Marshal(struct {
		Base   json.RawMessage
		Starts map[uint64]uint64
	}{base, starts})
}
