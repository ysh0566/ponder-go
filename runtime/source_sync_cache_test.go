package runtime

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

type sourceCountingCacheReader struct {
	reads    int
	children []*store.SourceAddressMap
	err      error
}

func (r *sourceCountingCacheReader) GetIntervals(context.Context, []filter.Object) (*store.SourceIntervalMap, error) {
	r.reads++
	return nil, r.err
}
func (r *sourceCountingCacheReader) GetChildAddresses(context.Context, filter.Object) (*store.SourceAddressMap, error) {
	r.reads++
	if r.err != nil {
		return nil, r.err
	}
	return r.children[r.reads-1], nil
}

// These are supplemental cache-ownership regressions, not original test credit.
func TestSourceSyncCacheOwnership(t *testing.T) {
	factory := filter.Object{"id": "factory", "type": "log", "chainId": 1, "address": "0x0000000000000000000000000000000000000000", "eventSelector": "0x0000000000000000000000000000000000000000000000000000000000000000", "childAddressLocation": "topic1", "fromBlock": 2, "toBlock": 5}
	f := filter.Object{"type": "log", "chainId": 1, "address": factory, "fromBlock": 10, "toBlock": 20}
	t.Run("disabled cache never reads stale database", func(t *testing.T) {
		reader := &sourceCountingCacheReader{err: errors.New("must not read")}
		children, err := GetSourceChildAddresses(context.Background(), false, []filter.Object{f}, reader)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := children.Entries("factory")
		if !ok || len(got) != 0 || reader.reads != 0 {
			t.Fatalf("children=%v ok=%v reads=%d", got, ok, reader.reads)
		}
		entries, err := GetSourceCachedIntervals(context.Background(), false, []filter.Object{f}, reader)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 || reader.reads != 0 {
			t.Fatalf("entries=%v reads=%d", entries, reader.reads)
		}
		if _, err := sourceCachedEntries(entries, f); err != nil {
			t.Fatal(err)
		}
		if _, err := sourceCachedEntries(entries, factory); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("repeated factory replaces not merges", func(t *testing.T) {
		reader := &sourceCountingCacheReader{children: []*store.SourceAddressMap{{Entries: []store.SourceChildAddress{{Address: "a", BlockNumber: 2}}}, {Entries: []store.SourceChildAddress{{Address: "b", BlockNumber: 9}}}}}
		result, err := GetSourceChildAddresses(context.Background(), true, []filter.Object{f, f}, reader)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := result.Entries("factory")
		want := []filter.ChildAddress{{Address: "b", Block: 9}}
		if reader.reads != 2 || !reflect.DeepEqual(got, want) {
			t.Fatalf("reads=%d result=%v", reader.reads, got)
		}
	})
	t.Run("parent invalidation does not mutate source intervals", func(t *testing.T) {
		cached := []store.SourceIntervalEntry{{Key: f, Values: []store.SourceFragmentIntervals{{Intervals: []store.SourceInterval{{10, 20}}}}}, {Key: factory, Values: []store.SourceFragmentIntervals{{Intervals: []store.SourceInterval{{2, 4}}}}}}
		got, err := RequiredSourceIntervalUnion([]filter.Object{f}, store.SourceInterval{0, 20}, cached)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []store.SourceInterval{{5, 5}, {10, 20}}) {
			t.Fatalf("missing=%v", got)
		}
		if !reflect.DeepEqual(cached[0].Values[0].Intervals, []store.SourceInterval{{10, 20}}) {
			t.Fatal("input cache mutated")
		}
		block, err := CachedSourceBlock([]filter.Object{f}, cached)
		if err != nil || block == nil || *block != 4 {
			t.Fatalf("block=%v error=%v", block, err)
		}
	})
	t.Run("empty filters retain source infinity", func(t *testing.T) {
		got, err := CachedSourceBlock(nil, nil)
		if err != nil || got == nil || !math.IsInf(*got, 1) {
			t.Fatalf("block=%v error=%v", got, err)
		}
	})
	t.Run("map keys require identity", func(t *testing.T) {
		clone := filter.Object{}
		for k, v := range f {
			clone[k] = v
		}
		_, err := CachedSourceBlock([]filter.Object{clone}, []store.SourceIntervalEntry{{Key: f}})
		if err == nil {
			t.Fatal("equal but distinct key accepted")
		}
	})
}
