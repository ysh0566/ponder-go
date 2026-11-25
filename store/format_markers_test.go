package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/schema"
)

func TestStoreFormatMarkers(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			b := sourceBackend(t, ctx, dialect)
			for _, tc := range []struct {
				name, change, inspect  string
				syncSchema, dropSchema bool
			}{
				{"fingerprint missing", "DELETE FROM %s._ponder_go_meta WHERE key='fingerprint'", "SELECT value FROM %s._ponder_go_meta WHERE key='fingerprint'", false, false},
				{"sync owner missing", "DELETE FROM %s._ponder_go_meta WHERE key='owner'", "SELECT value FROM %s._ponder_go_meta WHERE key='owner'", true, false},
				{"sync owner changed", "UPDATE %s._ponder_go_meta SET value='other' WHERE key='owner'", "SELECT value FROM %s._ponder_go_meta WHERE key='owner'", true, false},
				{"business schema missing", "DROP SCHEMA %s CASCADE", "", false, true},
				{"sync schema missing", "DROP SCHEMA %s CASCADE", "", true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ns := fmt.Sprintf("ponder_go_mark_%x", time.Now().UnixNano())
					s, err := New(ctx, b, model(false), ns, schema.Multichain)
					if err != nil {
						t.Fatal(err)
					}
					apply(t, s, "first", 1, 1, Row{"id": "kept", "balance": "7"}, Insert)
					target := ns
					if tc.syncSchema {
						target = s.SyncNamespace
					}
					if err := b.Exec(ctx, fmt.Sprintf(tc.change, schema.Quote(target))); err != nil {
						t.Fatal(err)
					}
					inspect := func() []map[string]any {
						t.Helper()
						var res Result
						var err error
						if tc.dropSchema {
							res, err = b.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", target)
						} else {
							res, err = b.Query(ctx, fmt.Sprintf(tc.inspect, schema.Quote(target)))
						}
						if err != nil {
							t.Fatal(err)
						}
						return res.Rows
					}
					before := inspect()
					_, err = New(ctx, b, model(false), ns, schema.Multichain)
					var formatError *MigrationError
					if !errors.As(err, &formatError) {
						t.Errorf("expected a migration error, got %v", err)
					}
					if after := inspect(); !reflect.DeepEqual(before, after) {
						t.Errorf("existing state changed from %v to %v", before, after)
					}
					if (!tc.dropSchema || tc.syncSchema) && balance(t, s, "kept") != "7" {
						t.Fatal("existing business row changed")
					}
				})
			}

			t.Run("lifecycle application metadata missing", func(t *testing.T) {
				ns := fmt.Sprintf("ponder_go_appmark_%x", time.Now().UnixNano())
				open := func() *Lifecycle {
					d, err := NewLifecycle(b, model(false), ns, schema.Multichain, LifecycleOptions{})
					if err != nil {
						t.Fatal(err)
					}
					return d
				}
				d := open()
				if _, err := d.Migrate(ctx, Migration{BuildID: "first"}); err != nil {
					t.Fatal(err)
				}
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
				app := d.s.internal("_ponder_go_app")
				if err := b.Exec(ctx, "DELETE FROM "+app); err != nil {
					t.Fatal(err)
				}
				next := open()
				defer next.Close()
				_, err := next.Migrate(ctx, Migration{BuildID: "second"})
				var formatError *MigrationError
				if !errors.As(err, &formatError) {
					t.Errorf("expected a missing application format error, got %v", err)
				}
				rows, err := b.Query(ctx, "SELECT key FROM "+app)
				if err != nil || len(rows.Rows) != 0 {
					t.Fatal("missing app metadata was replaced", rows, err)
				}
			})
		})
	}
}
