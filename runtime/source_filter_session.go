package runtime

import (
	"reflect"

	protocol "ponder.local/ponder-go/events"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

// Acquisition receives immutable copies of whole filters and their cached Map
// keys in the same graph. RecoverFilter may return its input (block filters), so
// merely holding a lock while planning would leak mutable canonical maps into
// asynchronous RPC workers. Include learning does not change acquisition bounds.
func sourceLocalPlanning(o SourceLocalHistoricalOptions, filters []filter.Object) ([]filter.Object, []store.SourceIntervalEntry) {
	if o.FilterSession == nil {
		return filters, o.CachedIntervals
	}
	roots := append([]filter.Object{}, filters...)
	for _, entry := range o.CachedIntervals {
		roots = append(roots, entry.Key)
	}
	snapshots := o.FilterSession.Snapshot(roots)
	cached := append([]store.SourceIntervalEntry{}, o.CachedIntervals...)
	for i := range cached {
		cached[i].Key = snapshots[len(filters)+i]
	}
	return snapshots[:len(filters)], cached
}

// Decoding may call a user logger, so it runs outside the filter lock. Temporary
// filter wrappers are used only for reads; emitted events retain the actual
// canonical callback and chain objects from the executable build.
func sourceHistoryDecodeSession(chain protocol.Object, callbacks []protocol.Object, session *filter.Session, decoder *protocol.Decoder, raw []protocol.RawEvent) ([]protocol.Object, error) {
	if session == nil {
		return decoder.DecodeEvents(chain, callbacks, raw)
	}
	snapshots := session.Callbacks(callbacks)
	original := make(map[uintptr]protocol.Object, len(callbacks))
	for i, snapshot := range snapshots {
		original[reflect.ValueOf(snapshot).Pointer()] = callbacks[i]
	}
	result, err := decoder.DecodeEvents(chain, snapshots, raw)
	if err != nil {
		return nil, err
	}
	for _, event := range result {
		if callback, ok := event["eventCallback"].(protocol.Object); ok {
			event["eventCallback"] = original[reflect.ValueOf(callback).Pointer()]
		}
	}
	return result, nil
}
