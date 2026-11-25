package store_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/store"
)

func TestSourceIntervalMapIdentity(t *testing.T) {
	factory := filter.Object{"type": "log", "eventSelector": "0x01", "childAddressLocation": "topic1"}
	f := filter.Object{"type": "log", "address": factory}
	other := filter.Object{"type": "log", "address": factory}
	values := []store.SourceFragmentIntervals{{Fragment: filter.Object{"type": "log"}, Intervals: []store.SourceInterval{{2, 5}}}}
	cached, err := store.NewSourceIntervalMap[store.SourceCacheEntry](
		store.FilterCacheEntry(f, nil),
		store.FactoryCacheEntry(factory, nil),
		store.FilterCacheEntry(other, nil),
		store.FilterCacheEntry(f, values),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Len() != 3 {
		t.Fatalf("entries = %d", cached.Len())
	}
	got, ok := cached.Get(f)
	if !ok || len(got) != 1 || got[0].Intervals[0] != (store.SourceInterval{2, 5}) {
		t.Fatalf("replacement = %v", got)
	}
	if got, ok := cached.Get(other); !ok || got != nil {
		t.Fatalf("second filter = %v, %v", got, ok)
	}
	if _, ok := cached.Get(filter.Object{"type": "log", "address": factory}); ok {
		t.Fatal("found distinct filter")
	}
	for i, entry := range cached.Entries() {
		entry.Key["position"] = i
	}
	if f["position"] != 0 || factory["position"] != 1 || other["position"] != 2 {
		t.Fatal("key identity or order changed")
	}

	for _, entry := range []store.SourceCacheEntry{
		store.FilterCacheEntry(factory, nil), store.FactoryCacheEntry(f, nil),
		store.FilterCacheEntry(filter.Object{"type": "other"}, nil),
		store.FactoryCacheEntry(filter.Object{"type": "trace", "eventSelector": "0x01", "childAddressLocation": "topic1"}, nil),
	} {
		if err := cached.Set(entry); err == nil || cached.Len() != 3 {
			t.Fatal("accepted an invalid cache key or mutated the map")
		}
	}
	partial, err := store.NewSourceIntervalMap[store.SourceCacheEntry](store.FilterCacheEntry(f, nil), store.FactoryCacheEntry(nil, nil))
	if partial != nil || err == nil {
		t.Fatal("invalid key returned a partial cache")
	}
	if err := cached.Set(nil); err == nil || cached.Len() != 3 {
		t.Fatal("nil entry changed cache")
	}
}

func TestSourceIntervalMapTypes(t *testing.T) {
	const prefix = `package caller
import ("ponder.local/ponder-go/store"; "ponder.local/ponder-go/filter")
func build(f, factory filter.Object, values []store.SourceFragmentIntervals) {
	child := store.FilterCacheEntry(f, values)
	parent := store.FactoryCacheEntry(factory, values)
	_, _ = child, parent
`
	for _, tc := range []struct {
		name, call  string
		diagnostics []string
	}{
		{"union", "_, _ = store.NewSourceIntervalMap[store.SourceCacheEntry](child, parent)", nil},
		{"inferred", "_, _ = store.NewSourceIntervalMap(child, parent)", []string{"factoryCacheEntry", "filterCacheEntry", "inferred type"}},
		{"outside_union", "_, _ = store.NewSourceIntervalMap[store.SourceCacheEntry](f)", []string{"does not implement store.SourceCacheEntry"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "caller.go")
			if err := os.WriteFile(file, []byte(prefix+tc.call+"\n}"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "caller.a"), file)
			output, err := cmd.CombinedOutput()
			if (err == nil) != (len(tc.diagnostics) == 0) {
				t.Fatalf("compile: %v\n%s", err, output)
			}
			for _, text := range tc.diagnostics {
				if !strings.Contains(string(output), text) {
					t.Fatalf("expected %q in diagnostics\n%s", text, output)
				}
			}
		})
	}
}
