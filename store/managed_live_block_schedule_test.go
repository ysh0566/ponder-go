package store

import (
	"encoding/json"
	"errors"
	"math/big"
	"slices"
	"testing"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/filter"
)

func TestSparseBlockScheduleEvidence(t *testing.T) {
	block := func(name string, interval, offset, from, to uint64) filter.Object {
		return filter.Object{"type": "block", "chainId": 1, "sourceId": name, "interval": interval, "offset": offset, "fromBlock": from, "toBlock": to}
	}
	filters := []filter.Object{block("Odd", 2, 1, 1, 7), block("Third", 3, 1, 4, 11)}
	for _, tc := range []struct {
		from, to uint64
		hit      bool
	}{
		{0, 0, false}, {1, 1, true}, {2, 2, false}, {3, 4, true}, {5, 5, true}, {6, 6, false}, {7, 7, true}, {8, 9, false}, {10, 10, true}, {11, 100, false},
	} {
		if err := checkEmptyBlockSchedules(filters, tc.from, tc.to); errors.Is(err, ErrManagedLivePendingActions) != tc.hit || (!tc.hit && err != nil) {
			t.Fatalf("range %d..%d: %v", tc.from, tc.to, err)
		}
	}
	opts := ManagedLiveOptions{runtimeLogFilters: map[uint64][]filter.Object{1: filters}}
	head := sparseRangeHead(1, 7)
	cp, _ := checkpoint.Decode(head.Checkpoint)
	cp.EventType, cp.EventIndex = checkpoint.Blocks, 0
	key := checkpoint.MustEncode(cp)
	b := ManagedLiveBlock{Head: head, LogEventCount: 2, LogFirstEvent: key, LogLastEvent: key}
	d := &ManagedLiveDomains{storage: &IndependentStorage{live: &ManagedLive{opts: opts}}}
	input := []RuntimeEventInput{{Kind: "block", Checkpoint: key, BlockSourceID: "Odd"}, {Kind: "block", Checkpoint: key, BlockSourceID: "Third"}}
	if err := d.ValidateRuntimeBlockInputs(b, input); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{}, {"Odd"}, {"Third", "Odd"}, {"Third", "Third"}, {"Odd", "Third", "Third"}} {
		values := make([]RuntimeEventInput, 0, len(ids))
		for _, id := range ids {
			v := input[0]
			v.BlockSourceID = id
			values = append(values, v)
		}
		bad := b
		bad.LogEventCount = uint64(len(values))
		if len(values) == 0 {
			bad.InputDigest = managedHash([]any{})
			bad.LogFirstEvent, bad.LogLastEvent = "", ""
		}
		if err := d.ValidateRuntimeBlockInputs(bad, values); !errors.Is(err, ErrManagedLiveBatchConflict) {
			t.Fatalf("accepted %v: %v", ids, err)
		}
	}
	if got := scheduledBlockSources(filters, 4); !slices.Equal(got, []string{"Third"}) {
		t.Fatal(got)
	}
	if sparseEventAllowed(checkpoint.Checkpoint{BlockTimestamp: cp.BlockTimestamp, ChainID: 1, BlockNumber: 6, TransactionIndex: cp.TransactionIndex, EventType: checkpoint.Blocks}, filters, "block") {
		t.Fatal("off-schedule block")
	}
}

func TestSignedBlockScheduleEvidence(t *testing.T) {
	for _, signed := range []any{int64(-3), float64(-3), json.Number("-3"), big.NewInt(-3)} {
		if n, ok := logOnlyQuantity(signed); ok || n != 0 {
			t.Fatalf("negative scan quantity %v accepted as %d", signed, n)
		}
		filters := []filter.Object{{"type": "block", "interval": signed, "offset": 2, "fromBlock": 2, "toBlock": 9}}
		for _, tc := range []struct {
			from, to uint64
			hit      bool
		}{
			{0, 1, false}, {2, 2, true}, {3, 4, false}, {4, 6, true}, {7, 7, false}, {8, 8, true}, {9, 15, false},
		} {
			err := checkEmptyBlockSchedules(filters, tc.from, tc.to)
			if tc.hit && !errors.Is(err, ErrManagedLivePendingActions) || !tc.hit && err != nil {
				t.Errorf("interval %v, range %d..%d: %v", signed, tc.from, tc.to, err)
			}
		}
	}
}
