package indexing

import (
	"fmt"

	"ponder.local/ponder-go/store"
)

// profileEvent is shared by dispatch and outside-transaction row prefetch. Count
// is the current successful-attempt count, not a prediction of batch success.
func (s *Service) profileEvent(event Object) (store.ProfileEvent, error) {
	_, name, _, err := callbackOf(event, false)
	if err != nil {
		return store.ProfileEvent{}, err
	}
	if s.options.ColumnAccessPattern[name] == nil {
		return store.ProfileEvent{}, fmt.Errorf("event %q lacks column profile", name)
	}
	chain, _ := event["chain"].(Object)
	id, err := clientInteger(chain["id"])
	if err != nil {
		return store.ProfileEvent{}, err
	}
	body, _ := event["event"].(Object)
	kind, _ := event["type"].(string)
	var fieldOrder map[string][]string
	if s.options.FieldOrder != nil {
		fieldOrder = s.options.FieldOrder(event)
	}
	return store.ProfileEvent{Callback: name, Count: s.options.EventCount[name], Type: kind, ChainID: id.Uint64(), Data: store.Row(body), FieldOrder: fieldOrder}, nil
}

// ProfileEvents projects actual decoded envelopes for row-cache prefetch. The
// session owner must call it serially outside dispatch; it does not increment
// counts, alter profiles, or bind database capabilities.
func (s *Service) ProfileEvents(events []Object) ([]store.ProfileEvent, error) {
	out := make([]store.ProfileEvent, len(events))
	for i, event := range events {
		value, err := s.profileEvent(event)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}
