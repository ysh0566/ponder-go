package store

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/filter"
	"github.com/ysh0566/ponder-go/schema"
)

func TestSourceQueryParameterBudget(t *testing.T) {
	for _, pair := range [][2]int{{0, 16000}, {4, 4}, {32, 32}, {65535, 65535}, {100000, 65535}} {
		got, err := SourceQueryParameterBudget(pair[0])
		if err != nil || got != pair[1] {
			t.Fatalf("budget %d: got %d, %v, want %d", pair[0], got, err, pair[1])
		}
	}
}

func TestSourceSyncBudgetSplitsLogs(t *testing.T) {
	var logs []filter.Object
	for _, vector := range protocolVectors(t) {
		if vector.Name != "logs/full" {
			continue
		}
		for i := range 5 {
			log := maps.Clone(vector.Input)
			log["logIndex"] = fmt.Sprintf("0x%x", i)
			logs = append(logs, log)
		}
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			b := &sourceLiveBackend{Backend: sourceBackend(t, ctx, dialect)}
			db, err := New(ctx, b, sourceActionsModel(), fmt.Sprintf("ponder_go_budget_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				t.Fatal(err)
			}
			cache, err := db.EnableProtocolCache(ctx)
			if err != nil {
				t.Fatal(err)
			}
			source, err := cache.SourceSyncStore(25)
			if err != nil {
				t.Fatal(err)
			}
			err = source.WithTransaction(ctx, func(bound *SourceSyncStore) error { return bound.InsertLogs(ctx, 1, logs) })
			if err != nil {
				t.Fatal(err)
			}
			var counts []int
			for _, q := range b.queries {
				if strings.HasPrefix(q.SQL, "INSERT INTO "+cache.table("logs")+"(") {
					counts = append(counts, len(q.Args))
				}
			}
			if !slices.Equal(counts, []int{24, 24, 12}) {
				t.Fatalf("bind parameter counts per log INSERT: %v", counts)
			}
			rows, err := b.Query(ctx, "SELECT count(*)::int AS n FROM "+cache.table("logs"))
			if err != nil || len(rows.Rows) != 1 || fmt.Sprint(rows.Rows[0]["n"]) != "5" {
				t.Fatalf("stored logs: %v, %v", rows, err)
			}
		})
	}
}
