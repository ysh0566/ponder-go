package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
)

func TestLogOnlyWireIsolation(t *testing.T) {
	for _, key := range []string{"omnichainEventCount", "omnichainFirstEvent", "omnichainLastEvent", "logOnlyLastEvent", "logOnlyScanHeads", "logOnlyScan"} {
		for _, wire := range []string{key, strings.ToUpper(key)} {
			t.Run(wire, func(t *testing.T) {
				raw := json.RawMessage(fmt.Sprintf("{\"before\":{\"nested\":[{\"%s\":null}]}}", wire))
				if rejectLogOnlyOperationFields([]managedLiveOperation{{Kind: "serve", Payload: raw}}) == nil {
					t.Fatal("non-log receipt accepted log-only wire key")
				}
			})
		}
	}
	if rejectLogOnlyJSON([]byte(`{"rowJSON":"{\"logOnlyScan\":null}"}`)) != nil {
		t.Fatal("opaque user row mistaken for receipt extension")
	}
}

func TestLogOnlyEmptyTailPagination(t *testing.T) {
	for _, name := range []string{"empty-all-pages", "later-log", "backwards", "beyond-end", "fractional", "nan", "query-error", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			calls := []float64{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			err := logOnlyEmptyTail(ctx, SourceEventDataArgs{FromBlock: 1, ToBlock: 3, Limit: 1}, func(_ context.Context, a SourceEventDataArgs) (SourceEventData, error) {
				calls = append(calls, a.FromBlock)
				r := SourceEventData{Cursor: a.FromBlock}
				switch name {
				case "later-log":
					if a.FromBlock == 3 {
						r.Logs = []filter.Object{{"blockNumber": 3}}
					}
				case "backwards":
					r.Cursor = a.FromBlock - 1
				case "beyond-end":
					r.Cursor = 4
				case "fractional":
					r.Cursor = 1.5
				case "nan":
					r.Cursor = math.NaN()
				case "query-error":
					return r, errors.New("read failed")
				}
				return r, nil
			})
			if (err == nil) != (name == "empty-all-pages") {
				t.Fatalf("pagination accepted=%v err=%v", name, err)
			}
			if name == "empty-all-pages" || name == "later-log" {
				if !reflect.DeepEqual(calls, []float64{1, 2, 3}) {
					t.Fatalf("did not inspect later pages: %v", calls)
				}
			}
			if name == "cancelled" && len(calls) != 0 {
				t.Fatal("query admitted after cancellation")
			}
		})
	}
}

func logOnlyScanFixture(t *testing.T) (*ManagedLive, ManagedLiveControl, map[uint64]managedLogOnlyScan) {
	t.Helper()
	f := filter.Object{"type": "log", "chainId": 1, "fromBlock": 1, "toBlock": 3}
	m := &ManagedLive{opts: ManagedLiveOptions{ManagedFiniteOptions: ManagedFiniteOptions{Chains: []MigrationChain{{ID: 1}}, EndBlocks: map[uint64]uint64{1: 3}}, runtimeOmnichain: true, runtimeStarts: map[uint64]uint64{1: 1}, runtimeLogFilters: map[uint64][]filter.Object{1: {f}}}}
	heads := []ManagedLiveHead{}
	for n := uint64(1); n <= 4; n++ {
		heads = append(heads, ManagedLiveHead{Checkpoint: checkpoint.MustEncode(checkpoint.Block(100+n, 1, n, true)), Hash: fmt.Sprintf("0x%064x", n), ParentHash: fmt.Sprintf("0x%064x", n-1)})
	}
	c := ManagedLiveControl{Version: managedLivePagedLogOnlyVersion, Batches: 1, Cursors: map[uint64]ManagedLiveHead{1: heads[0]}, FinalizedHeads: map[uint64]ManagedLiveHead{1: heads[3]}, HistoryCuts: map[uint64]uint64{1: 3}, LogicalCursors: map[uint64]string{1: checkpoint.MustEncode(checkpoint.Block(101, 1, 1, false))}}
	fragments, err := filter.GetFragments(f)
	if err != nil || len(fragments) == 0 {
		t.Fatalf("fragments %v %v", fragments, err)
	}
	coverage := []managedLogOnlyFragment{}
	for _, p := range fragments {
		coverage = append(coverage, managedLogOnlyFragment{ID: filter.EncodeFragment(p["fragment"].(filter.Object)), Intervals: []SourceInterval{{1, 3}}})
	}
	scans, err := m.acquiredScanRanges(context.Background(), c, func(id, height uint64) (ManagedLiveHead, error) {
		if id != 1 || height < 1 || height > uint64(len(heads)) {
			return ManagedLiveHead{}, fmt.Errorf("unexpected header %d/%d", id, height)
		}
		return heads[height-1], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scan := scans[1]
	scan.FiltersHash = managedHash([]filter.Object{f})
	scan.Coverage = [][]managedLogOnlyFragment{coverage}
	scan.EmptyFrom = 1
	scans[1] = scan
	return m, c, scans
}

func TestLogOnlyScanReceiptBinding(t *testing.T) {
	for _, name := range []string{"valid", "missing-chain", "foreign-chain", "filter", "missing-fragment", "coverage-gap", "malformed-after-covered", "missing-final", "broken-parent", "skipped-header", "wrong-empty-start", "stale-scan-end", "wrong-end-cut"} {
		t.Run(name, func(t *testing.T) {
			m, c, scans := logOnlyScanFixture(t)
			scan := scans[1]
			switch name {
			case "missing-chain":
				delete(scans, 1)
			case "foreign-chain":
				scans[2] = scan
			case "filter":
				scan.FiltersHash = managedHash("different filter")
			case "missing-fragment":
				scan.Coverage[0] = nil
			case "coverage-gap":
				scan.Coverage[0][0].Intervals = []SourceInterval{{1, 1}, {3, 3}}
			case "malformed-after-covered":
				scan.Coverage[0][0].Intervals = []SourceInterval{{1, 3}, {5, 4}}
			case "missing-final":
				scan.FinalTail = nil
			case "broken-parent":
				scan.FinalTail.First.ParentHash = fmt.Sprintf("0x%064x", 99)
			case "skipped-header":
				scan.FinalTail.First = scan.Scanned.Last
			case "wrong-empty-start":
				scan.EmptyFrom = 2
			case "stale-scan-end":
				h := scan.Scanned.Last
				h.Hash = fmt.Sprintf("0x%064x", 99)
				c.LogOnlyScanHeads = map[uint64]ManagedLiveHead{1: h}
			case "wrong-end-cut":
				c.HistoryCuts[1] = 2
			}
			if name != "missing-chain" {
				scans[1] = scan
			}
			ends, err := m.validateLogOnlyScan(c, scans)
			if (err == nil) != (name == "valid") {
				t.Fatalf("receipt %s ends%v err%v", name, ends, err)
			}
			if name == "valid" && ends[1] != scan.Scanned.Last {
				t.Fatal("source scan endpoint changed")
			}
		})
	}
}

func TestLogOnlyServeRetryLineage(t *testing.T) {
	_, _, scans := logOnlyScanFixture(t)
	_, _, identical := logOnlyScanFixture(t)
	if !samePagedScanInputs(scans, identical) {
		t.Fatal("exact retry rejected")
	}
	if samePagedScanInputs(scans, nil) {
		t.Fatal("missing ranges accepted")
	}
	identical[1].Scanned.Digest = managedHash("different headers")
	if samePagedScanInputs(scans, identical) {
		t.Fatal("changed lineage retry accepted")
	}
}

func TestLogOnlyPhysicalInputs(t *testing.T) {
	m, _, scans := logOnlyScanFixture(t)
	head := scans[1].Scanned.First
	cp, _ := checkpoint.Decode(head.Checkpoint)
	cp.TransactionIndex = 0
	cp.EventType = checkpoint.Logs
	cp.EventIndex = 0
	event := checkpoint.MustEncode(cp)
	base := ManagedLiveBlock{Head: head, InputDigest: managedHash("actual encoded input"), LogEventCount: 1, LogFirstEvent: event, LogLastEvent: event, SourceCheckpoint: event}
	for _, name := range []string{"valid", "empty-valid", "empty-with-events", "empty-with-digest", "wrong-chain", "wrong-height", "wrong-kind", "reverse", "source-not-last", "legacy-fields"} {
		t.Run(name, func(t *testing.T) {
			opts := m.opts
			b := base
			switch name {
			case "empty-valid", "empty-with-events", "empty-with-digest":
				b.LogEventCount = 0
				b.LogFirstEvent = ""
				b.LogLastEvent = ""
				b.SourceCheckpoint = ""
				b.InputDigest = managedHash([]any{})
				if name == "empty-with-events" {
					b.LogLastEvent = event
				}
				if name == "empty-with-digest" {
					b.InputDigest = managedHash("different")
				}
			case "wrong-chain":
				p := cp
				p.ChainID = 2
				b.LogFirstEvent = checkpoint.MustEncode(p)
			case "wrong-height":
				p := cp
				p.BlockNumber = 2
				b.LogLastEvent = checkpoint.MustEncode(p)
			case "wrong-kind":
				p := cp
				p.EventType = checkpoint.Transactions
				b.LogFirstEvent = checkpoint.MustEncode(p)
			case "reverse":
				p := cp
				p.EventIndex = 1
				b.LogFirstEvent = checkpoint.MustEncode(p)
			case "source-not-last":
				p := cp
				p.EventIndex = 1
				b.SourceCheckpoint = checkpoint.MustEncode(p)
			case "legacy-fields":
				opts.runtimeLogFilters = nil
			}
			err := validateLogOnlyBlock(opts, b)
			want := name == "valid" || name == "empty-valid"
			if (err == nil) != want {
				t.Fatalf("%s err%v", name, err)
			}
		})
	}
}
