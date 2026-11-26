package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ysh0566/ponder-go/filter"
)

// A log at height 4 is followed by two scan pages and a separate finality anchor.
const activationLogEnd = 4 + lineagePageSize

func configureActivationLogRange(runtime *ManagedLiveRuntimeOptions, starts map[uint64]ManagedLiveHead) ManagedLiveHeaderReader {
	runtime.EndBlocks = map[uint64]uint64{}
	runtime.LogOnlyFilters = map[uint64][]filter.Object{}
	headers := map[uint64]map[uint64]ManagedLiveHead{}
	for chain, head := range starts {
		runtime.EndBlocks[chain] = activationLogEnd
		runtime.LogOnlyFilters[chain] = []filter.Object{{"type": "log", "chainId": chain, "fromBlock": 4, "toBlock": activationLogEnd}}
		headers[chain] = map[uint64]ManagedLiveHead{4: head}
		for n := uint64(5); n <= activationLogEnd+1; n++ {
			head = liveSegmentsUnitBlock(chain, n, head.Hash).Head
			headers[chain][n] = head
		}
		runtime.FinalizedHeads[chain] = head
	}
	return func(chain, height uint64) (ManagedLiveHead, error) {
		head, ok := headers[chain][height]
		if !ok {
			return head, fmt.Errorf("unexpected header %d/%d", chain, height)
		}
		return head, nil
	}
}

func checkActivationScanHeads(t *testing.T, c ManagedLiveControl, committed bool, get ManagedLiveHeaderReader) {
	t.Helper()
	if !committed {
		if len(c.LogOnlyScanHeads) != 0 {
			t.Fatalf("uncommitted scan endpoints survived: %v", c.LogOnlyScanHeads)
		}
		return
	}
	if len(c.LogOnlyScanHeads) != 2 {
		t.Fatalf("scan endpoints: %v", c.LogOnlyScanHeads)
	}
	for _, chain := range []uint64{1, 10} {
		end, err := get(chain, activationLogEnd)
		if err != nil || c.LogOnlyScanHeads[chain] != end {
			t.Fatalf("chain %d scan endpoint %v, want %v: %v", chain, c.LogOnlyScanHeads[chain], end, err)
		}
	}
}

func checkActivationLogPages(t *testing.T, ctx context.Context, q Querier, s *Store, operation string, committed bool, get ManagedLiveHeaderReader) {
	t.Helper()
	r, err := q.Query(ctx, "SELECT chain_id::text AS chain,first_block::text AS first,headers::text AS headers FROM "+
		s.internal("_ponder_go_live_lineage_pages")+" WHERE operation_id=$1 ORDER BY chain_id,first_block", operation)
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		if len(r.Rows) != 0 {
			t.Fatalf("operation %s retained %d uncommitted scan pages", operation, len(r.Rows))
		}
		return
	}
	// Each chain spans a full page, one more scanned header and a finality tail.
	if len(r.Rows) != 6 {
		t.Fatalf("operation %s stored %d pages, want 6", operation, len(r.Rows))
	}
	for i, chain := range []uint64{1, 10} {
		for j, page := range []struct{ first, count uint64 }{{4, lineagePageSize}, {activationLogEnd, 1}, {activationLogEnd + 1, 1}} {
			row := r.Rows[3*i+j]
			var headers []ManagedLiveHead
			err := json.Unmarshal([]byte(fmt.Sprint(row["headers"])), &headers)
			if err != nil || row["chain"] != fmt.Sprint(chain) || row["first"] != fmt.Sprint(page.first) || uint64(len(headers)) != page.count {
				t.Fatalf("operation %s chain %v page %v length %d: %v", operation, row["chain"], row["first"], len(headers), err)
			}
			for k, h := range headers {
				height := page.first + uint64(k)
				want, err := get(chain, height)
				if err != nil || h != want {
					t.Fatalf("operation %s chain %d height %d: got %v want %v: %v", operation, chain, height, h, want, err)
				}
			}
		}
	}
}
