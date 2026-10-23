package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/schema"
)

func lifecycleSourceModel(complex bool) schema.Schema {
	m := sourceActionsModel()
	if !complex {
		return m
	}
	m.Enums = []schema.Enum{{Name: "mood", Values: []string{"sad", "happy"}}}
	m.Tables = append(m.Tables,
		schema.Table{Name: "kyle", Columns: []schema.Column{{Name: "age", Type: "integer", Primary: true}, {Name: "mood", Type: "mood", NotNull: true}}},
		schema.Table{Name: "table", Columns: []schema.Column{{Name: "name", Type: "text"}, {Name: "age", Type: "integer"}, {Name: "address", Type: "hex"}}, PrimaryKey: []string{"name", "address"}},
	)
	fields := []schema.ViewField{}
	for _, c := range m.Tables[2].Columns {
		column := c
		fields = append(fields, schema.ViewField{Name: c.Name, Column: &column})
	}
	m.Views = []schema.View{{Name: "user_view", Fields: fields, Query: `SELECT * FROM "table"`}}
	return m
}

// Original physical names have explicit native counterparts. This queries the
// real database catalog; source per-table reorg storage is the persisted native
// global journal, and the app metadata is separate from its ownership marker.
func expectLifecycleRelation(t *testing.T, d *Lifecycle, source string, view bool) {
	t.Helper()
	name := source
	if source == "_ponder_meta" {
		name = "_ponder_go_app"
	}
	if source == "_ponder_checkpoint" {
		name = "_ponder_go_progress"
	}
	if source == "_reorg__account" || source == "_reorg__kyle" {
		name = "_ponder_go_journal"
	}
	kind := "BASE TABLE"
	if view {
		kind = "VIEW"
	}
	r, e := d.s.Backend.Query(context.Background(), "SELECT table_name FROM information_schema.tables WHERE table_schema=$1 AND table_type=$2 AND table_name=$3", d.s.Namespace, kind, name)
	if e != nil || len(r.Rows) != 1 {
		t.Fatalf("source toContain(%q) mapped to real %s %q: %#v %v", source, kind, name, r, e)
	}
}

func TestUpstreamDatabaseLifecycle(t *testing.T) {
	for _, dialect := range []string{"pglite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, name := range []string{"migrate() succeeds with empty schema", "migrate() with empty schema creates tables, views, and enums", "migrate() throws with schema used", "migrate() throws with schema used after waiting for lock", "migrate() succeeds with crash recovery", "migrate() succeeds with crash recovery after waiting for lock", "migrate() with crash recovery drops indexes and triggers", "heartbeat updates the heartbeat_at value"} {
				// The original returns before its only assertion on PGlite because
				// it cannot open two independent clients to one PGlite data dir.
				if dialect == "pglite" && name == "migrate() throws with schema used after waiting for lock" {
					continue
				}
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					b := sourceBackend(t, ctx, dialect)
					m := lifecycleSourceModel(name == "migrate() with empty schema creates tables, views, and enums")
					if name == "migrate() with crash recovery drops indexes and triggers" {
						m.Tables[0].Indexes = []schema.Index{{Name: "account_balance_index", Columns: []string{"balance"}}}
					}
					opts := LifecycleOptions{}
					switch name {
					case "migrate() throws with schema used after waiting for lock":
						opts.HeartbeatInterval = 250 * time.Millisecond
						opts.HeartbeatTimeout = 1000 * time.Millisecond
					case "migrate() succeeds with crash recovery after waiting for lock":
						opts.HeartbeatInterval = 750 * time.Millisecond
						opts.HeartbeatTimeout = 500 * time.Millisecond
					case "heartbeat updates the heartbeat_at value":
						opts.HeartbeatInterval = 250 * time.Millisecond
						opts.HeartbeatTimeout = 625 * time.Millisecond
					}
					ns := fmt.Sprintf("ponder_go_life_%x", time.Now().UnixNano())
					d, e := NewLifecycle(b, m, ns, schema.Multichain, opts)
					if e != nil {
						t.Fatal(e)
					}
					defer d.Close()
					if _, e = d.Migrate(ctx, Migration{BuildID: "abc"}); e != nil {
						t.Fatal(e)
					}
					switch name {
					case "migrate() succeeds with empty schema":
						for _, relation := range []string{"account", "_reorg__account", "_ponder_meta"} {
							expectLifecycleRelation(t, d, relation, false)
						}
						metadata, e := d.Metadata(ctx)
						if e != nil || len(metadata) != 1 {
							t.Fatalf("source metadata length=1: %#v %v", metadata, e)
						}
					case "migrate() with empty schema creates tables, views, and enums":
						// Repeated kyle/reorg assertions are deliberately preserved.
						for _, relation := range []string{"account", "_reorg__account", "kyle", "_reorg__kyle", "kyle", "_reorg__kyle", "_ponder_meta", "_ponder_checkpoint"} {
							expectLifecycleRelation(t, d, relation, false)
						}
						expectLifecycleRelation(t, d, "user_view", true)
					case "heartbeat updates the heartbeat_at value":
						before, e := d.Metadata(ctx)
						if e != nil {
							t.Fatal(e)
						}
						<-time.After(500 * time.Millisecond)
						after, e := d.Metadata(ctx)
						if e != nil {
							t.Fatal(e)
						}
						if after[0].Value.HeartbeatAt <= before[0].Value.HeartbeatAt {
							t.Fatalf("source heartbeat greater-than: %d <= %d", after[0].Value.HeartbeatAt, before[0].Value.HeartbeatAt)
						}
					default:
						secondBuild := "abc"
						if name == "migrate() throws with schema used" {
							secondBuild = "def"
						}
						migration := Migration{BuildID: secondBuild}
						if name == "migrate() with crash recovery drops indexes and triggers" {
							if e := d.Actions().CreateIndexes(ctx); e != nil {
								t.Fatal(e)
							}
							cp := checkpoint.MustEncode(actionCheckpoint(10))
							if e := d.Actions().InitializeCheckpoints(ctx, []CheckpointState{{ChainID: 1, ChainName: "mainnet", LatestCheckpoint: cp, FinalizedCheckpoint: cp, SafeCheckpoint: cp}}); e != nil {
								t.Fatal(e)
							}
							if e := d.MarkReady(ctx); e != nil {
								t.Fatal(e)
							}
							migration.Chains = []MigrationChain{{ID: 1, Name: "mainnet"}}
							migration.FinalizedBlocks = []FinalizedBlock{{Number: 10, Timestamp: 1, Hash: "0x", ParentHash: "0x"}}
						}
						if name != "migrate() throws with schema used after waiting for lock" && name != "migrate() succeeds with crash recovery after waiting for lock" {
							if e := d.Close(); e != nil {
								t.Fatal(e)
							}
						}
						second, e := NewLifecycle(b, m, ns, schema.Multichain, opts)
						if e != nil {
							t.Fatal(e)
						}
						defer second.Close()
						_, e = second.Migrate(ctx, migration)
						if name == "migrate() throws with schema used" || name == "migrate() throws with schema used after waiting for lock" {
							if e == nil {
								t.Fatal("source error toBeDefined")
							}
							return
						}
						if e != nil {
							t.Fatal(e)
						}
						if name == "migrate() succeeds with crash recovery" {
							metadata, e := second.Metadata(ctx)
							if e != nil || len(metadata) != 1 {
								t.Fatalf("source metadata length=1: %#v %v", metadata, e)
							}
							for _, relation := range []string{"account", "_reorg__account", "_ponder_meta"} {
								expectLifecycleRelation(t, second, relation, false)
							}
						}
						if name == "migrate() with crash recovery drops indexes and triggers" {
							r, e := b.Query(ctx, "SELECT indexname FROM pg_indexes WHERE schemaname=$1 AND tablename='account'", ns)
							if e != nil || len(r.Rows) != 1 {
								t.Fatalf("source user index length=1: %#v %v", r, e)
							}
						}
					}
				})
			}
		})
	}
}
