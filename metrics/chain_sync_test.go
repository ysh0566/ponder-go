package metrics

import "testing"

func TestChainSyncGauges(t *testing.T) {
	r := New()
	labels := map[string]string{"chain": "a"}
	ctx := WithChainSync(t.Context(), r)
	ChainSync(WithChainSync(ctx, nil)).SetSyncBlock("unused", 4, 40)
	expectSamples(t, r, "ponder_sync_block")
	ChainSync(ctx).SetSyncBlock("a", 3, 120)
	r.SetSyncBlock("a", 2, 100)
	expectSamples(t, r, "ponder_sync_block", sample{labels, 2})
	expectSamples(t, r, "ponder_sync_block_timestamp", sample{labels, 100})

	r.SetSyncPhase("a", true)
	expectSamples(t, r, "ponder_sync_is_complete", sample{labels, 1})
	expectSamples(t, r, "ponder_sync_is_realtime")
	r.SetSyncPhase("a", false)
	expectSamples(t, r, "ponder_sync_is_complete")
	expectSamples(t, r, "ponder_sync_is_realtime", sample{labels, 1})
	r.SetSyncPhase("a", true)
	r.SetSyncPhase("a", true)
	expectSamples(t, r, "ponder_sync_is_realtime", sample{labels, 0})
	expectSamples(t, r, "ponder_sync_is_complete", sample{labels, 1})
	r.ResetIndexing()
	for _, name := range []string{"ponder_sync_block", "ponder_sync_is_complete", "ponder_sync_is_realtime"} {
		expectSamples(t, r, name)
	}
	expectSamples(t, r, "ponder_sync_block_timestamp", sample{labels, 100})
}

func TestIndexingTimeline(t *testing.T) {
	r := New()
	a, b := map[string]string{"chain": "a"}, map[string]string{"chain": "b"}
	names := map[uint64]string{1: "a", 2: "b"}
	global := NewIndexingTimeline(names, true)
	local := NewIndexingTimeline(names, false)
	const family = "ponder_indexing_timestamp"
	global.Commit(nil, 1, 15)
	global.Commit(r, 3, 100)
	expectSamples(t, r, family)
	global.Commit(r, 2, 30)
	expectSamples(t, r, family, sample{a, 30}, sample{b, 30})
	positions := map[uint64]uint64{1: 10, 2: 20}
	get := func(id uint64) (uint64, bool) { v, ok := positions[id]; return v, ok }
	global.Restore(r, get)
	expectSamples(t, r, family, sample{a, 20}, sample{b, 20})
	local.Restore(r, get)
	expectSamples(t, r, family, sample{a, 10}, sample{b, 20})
	delete(positions, 2)
	positions[1] = 0
	local.Restore(r, get)
	expectSamples(t, r, family, sample{a, 0})
	delete(positions, 1)
	global.Restore(r, get)
	expectSamples(t, r, family)
	local.Commit(r, 1, 40)
	local.Commit(r, 2, 20)
	expectSamples(t, r, family, sample{a, 40}, sample{b, 20})
	r.ResetIndexing()
	expectSamples(t, r, family)
}
