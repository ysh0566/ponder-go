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
