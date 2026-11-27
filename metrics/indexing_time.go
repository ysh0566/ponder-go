package metrics

// IndexingTimeline maps participating chain IDs to the writer's progress labels.
// Omnichain commits a single ordered prefix shared by all participating chains.
type IndexingTimeline struct {
	names  map[uint64]string
	shared bool
}

// NewIndexingTimeline takes ownership of the supplied ID-to-name map.
func NewIndexingTimeline(names map[uint64]string, shared bool) IndexingTimeline {
	return IndexingTimeline{names: names, shared: shared}
}

// Commit publishes the block time after a group of events commits.
func (t IndexingTimeline) Commit(r *Registry, chain, timestamp uint64) {
	if r == nil {
		return
	}
	name, ok := t.names[chain]
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !t.shared {
		r.indexTime.WithLabelValues(name).Set(float64(timestamp))
		return
	}
	for _, name := range t.names {
		r.indexTime.WithLabelValues(name).Set(float64(timestamp))
	}
}

// Restore replaces the projection after recovery or a rewind. An empty history
// has no sample. Timestamp zero is a valid genesis block time. The lookup reads
// an immutable progress snapshot while Restore holds the registry lock.
func (t IndexingTimeline) Restore(r *Registry, position func(uint64) (uint64, bool)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest uint64
	var anyPosition bool
	for id, name := range t.names {
		ts, ok := position(id)
		if t.shared {
			if ok {
				latest = max(latest, ts)
				anyPosition = true
			}
		} else if ok {
			r.indexTime.WithLabelValues(name).Set(float64(ts))
		} else {
			r.indexTime.DeleteLabelValues(name)
		}
	}
	if t.shared {
		for _, name := range t.names {
			if anyPosition {
				r.indexTime.WithLabelValues(name).Set(float64(latest))
			} else {
				r.indexTime.DeleteLabelValues(name)
			}
		}
	}
}
