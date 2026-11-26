package store

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/ysh0566/ponder-go/filter"
)

func TestLogOnlyRangeCoverage(t *testing.T) {
	m, _, _ := logOnlyScanFixture(t)
	makeFilter := func(start, end uint64) filter.Object {
		return filter.Object{"type": "log", "chainId": 1, "fromBlock": start, "toBlock": end}
	}
	for _, tc := range []struct {
		name   string
		bounds [][2]uint64
		valid  bool
	}{
		{"disjoint", [][2]uint64{{1, 1}, {3, 3}}, true},
		{"overlap", [][2]uint64{{2, 3}, {1, 2}}, true},
		{"inverted", [][2]uint64{{1, 3}, {3, 2}}, false},
		{"early-start", [][2]uint64{{0, 1}, {2, 3}}, false},
		{"unused-start", [][2]uint64{{2, 2}, {2, 3}}, false},
		{"unused-end", [][2]uint64{{1, 1}, {1, 2}}, false},
		{"late-end", [][2]uint64{{1, 2}, {3, 4}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var group []filter.Object
			for _, bounds := range tc.bounds {
				group = append(group, makeFilter(bounds[0], bounds[1]))
			}
			_, err := normalizeLogOnlyFilters(map[uint64][]filter.Object{1: group}, m.opts)
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v err=%v", err == nil, err)
			}
		})
	}
	group := []filter.Object{makeFilter(1, 1), makeFilter(3, 4)}
	var coverage [][]managedLogOnlyFragment
	for _, f := range group {
		fragments, err := filter.GetFragments(f)
		if err != nil {
			t.Fatal(err)
		}
		var proof []managedLogOnlyFragment
		for _, part := range fragments {
			proof = append(proof, managedLogOnlyFragment{ID: filter.EncodeFragment(part["fragment"].(filter.Object)), Intervals: []SourceInterval{{filter.FilterFromBlock(f), filter.FilterToBlock(f)}}})
		}
		coverage = append(coverage, proof)
	}
	if err := validateLogCoverage(group, coverage, 1, 4); err != nil {
		t.Fatal(err)
	}
	coverage[1][0].Intervals = []SourceInterval{{3, 3}}
	if err := validateLogCoverage(group, coverage, 1, 4); !errors.Is(err, ErrManagedLivePendingActions) {
		t.Fatalf("missing height 4 accepted: %v", err)
	}
	if err := validateLogCoverage(group, coverage, 2, 3); err != nil {
		t.Fatal(err)
	}
	coverage[0][0].Intervals = nil
	if err := validateLogCoverage(group, coverage, 2, 2); err != nil {
		t.Fatalf("unconfigured gap: %v", err)
	}
	coverage[0][0].Intervals = []SourceInterval{{math.NaN(), 1}}
	if err := validateLogCoverage(group, coverage, 2, 2); err == nil {
		t.Fatal("malformed interval accepted in unconfigured gap")
	}
}

func TestLogOnlyRangeScanPages(t *testing.T) {
	filters := []filter.Object{
		{"type": "log", "chainId": 1, "fromBlock": 0, "toBlock": 0},
		{"type": "log", "chainId": 1, "fromBlock": 2, "toBlock": 4},
		{"type": "log", "chainId": 1, "fromBlock": 3, "toBlock": 5},
	}
	o := ManagedLiveOptions{runtimeLogFilters: map[uint64][]filter.Object{1: filters}}
	args := SourceEventDataArgs{ChainID: 1, Filters: filters, FromBlock: 0, ToBlock: 5, Limit: 1}
	var pages []SourceInterval
	digest, err := o.logOnlyScanHash(context.Background(), args, func(_ context.Context, page SourceEventDataArgs) (SourceEventData, error) {
		pages = append(pages, SourceInterval{page.FromBlock, page.ToBlock})
		return SourceEventData{Cursor: page.ToBlock}, nil
	})
	if err != nil || !o.validLogScanHash(1, digest) || !slices.Equal(pages, []SourceInterval{{0, 0}, {1, 1}, {2, 2}, {3, 4}, {5, 5}}) {
		t.Fatalf("pages=%v hash=%s err=%v", pages, digest, err)
	}
	_, err = o.logOnlyScanHash(context.Background(), args, func(context.Context, SourceEventDataArgs) (SourceEventData, error) {
		return SourceEventData{Cursor: args.ToBlock}, nil
	})
	if !errors.Is(err, ErrManagedLiveBatchConflict) {
		t.Fatalf("accepted cursor crossing filter boundary: %v", err)
	}
}
