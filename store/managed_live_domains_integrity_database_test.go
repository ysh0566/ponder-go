package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"ponder.local/ponder-go/schema"
)

type liveDomainRawBackend struct {
	Backend
	once sync.Once
	err  error
}

func (b *liveDomainRawBackend) Close() error {
	b.once.Do(func() { b.err = b.Backend.Close() })
	return b.err
}
func liveDomainsRawOpen(t *testing.T, o IndependentStorageOptions, dialect string, business bool) Backend {
	t.Helper()
	var b Backend
	var e error
	if dialect == "postgres" {
		b, e = OpenPostgres(context.Background(), o.PostgresURL)
	} else {
		directory := o.SyncDirectory
		if business {
			directory = o.BusinessDirectory
		}
		b, e = OpenPGlite(context.Background(), directory)
	}
	if e != nil {
		t.Fatal(e)
	}
	b = &liveDomainRawBackend{Backend: b}
	t.Cleanup(func() {
		if e := b.Close(); e != nil {
			t.Error(e)
		}
	})
	return b
}

func TestManagedLiveDomainsBootstrap(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, mode := range []string{"before_commit", "after_commit"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					o, opts, open := liveDomainsFixture(t, dialect)
					opts.recovery = true
					opts.coordinated = true
					normalized, e := normalizeManagedLiveOptions(o, opts)
					if e != nil {
						t.Fatal(e)
					}
					normalized.recovery = true
					normalized.coordinated = true
					business, syncBackend := liveDomainsRawOpen(t, o, dialect, true), liveDomainsRawOpen(t, o, dialect, false)
					fault := &liveDomainFaultBackend{Backend: syncBackend, phase: "bootstrap", mode: mode}
					bare := &IndependentStorage{businessBackend: business, syncBackend: fault}
					if e = prepareLiveDomains(ctx, bare, o, normalized); e == nil || !fault.fired {
						t.Fatalf("bootstrap fault missed %v", e)
					}
					r, e := syncBackend.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.SyncNamespace)
					if e != nil {
						t.Fatal(e)
					}
					want := 0
					if mode == "after_commit" {
						want = 1
					}
					if len(r.Rows) != want {
						t.Fatalf("bootstrap partial DDL survived rollback %v", r.Rows)
					}
					bare.syncBackend = syncBackend
					if e = prepareLiveDomains(ctx, bare, o, normalized); e != nil {
						t.Fatalf("bootstrap retry=%v", e)
					}
					if e = business.Close(); e != nil {
						t.Fatal(e)
					}
					if e = syncBackend.Close(); e != nil {
						t.Fatal(e)
					}
					d := open(opts)
					c, e := d.Control(ctx)
					if e != nil || c.Generation != 1 || c.Version != managedLiveDomainsVersion {
						t.Fatalf("first business open after bootstrap retry=%+v %v", c, e)
					}
					l, e := d.readLedger(ctx, d.storage.syncBackend)
					if e != nil || l.Binding == nil || len(l.Records) != 1 {
						t.Fatalf("binding=%+v %v", l, e)
					}
				})
			}
		})
	}
}

func TestManagedLiveDomainsIntegrity(t *testing.T) {
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			for _, mode := range []string{"missing_sync", "missing_business", "sync_catalog", "rpc_trigger", "impossible_intent", "impossible_orphan", "native_meta_value", "native_meta_missing", "cache_owner"} {
				t.Run(mode, func(t *testing.T) {
					ctx := context.Background()
					o, opts, open := liveDomainsFixture(t, dialect)
					d := open(opts)
					_, request := liveDomainsSeed(t, d)
					liveDomainsCacheSeed(t, d)
					m := d.live()
					c, e := m.Control(ctx)
					if e != nil {
						t.Fatal(e)
					}
					before, e := m.snapshotHash(ctx, d.storage.businessBackend, c.LastOperation)
					if e != nil {
						t.Fatal(e)
					}
					mutationBusiness := d.storage.businessBackend
					if mode == "missing_business" {
						if e = d.Close(ctx); e != nil {
							t.Fatal(e)
						}
						mutationBusiness = liveDomainsRawOpen(t, o, dialect, true)
					}
					switch mode {
					case "missing_sync":
						e = d.storage.syncBackend.Exec(ctx, "ALTER SCHEMA "+schema.Quote(o.SyncNamespace)+" RENAME TO "+schema.Quote(o.SyncNamespace+"_saved"))
					case "missing_business":
						e = mutationBusiness.Exec(ctx, "ALTER SCHEMA "+schema.Quote(o.BusinessNamespace)+" RENAME TO "+schema.Quote(o.BusinessNamespace+"_saved"))
					case "sync_catalog":
						e = d.storage.syncBackend.Exec(ctx, "ALTER TABLE "+d.table("log")+" ADD COLUMN unexpected boolean")
					case "rpc_trigger":
						cache := d.storage.syncCache
						e = d.storage.syncBackend.Exec(ctx, "CREATE FUNCTION "+cache.table("invalid_prune")+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN DELETE FROM "+cache.table("rpc_request_results")+" WHERE block_number IS NULL OR chain_id=1; RETURN OLD; END $$")
						if e == nil {
							e = d.storage.syncBackend.Exec(ctx, "CREATE TRIGGER invalid_prune AFTER DELETE ON "+cache.table("rpc_request_results")+" FOR EACH STATEMENT EXECUTE FUNCTION "+cache.table("invalid_prune")+"()")
						}
					case "native_meta_value":
						e = d.storage.syncBackend.Exec(ctx, "UPDATE "+schema.Quote(o.SyncNamespace)+"._ponder_go_meta SET value='invalid' WHERE key='fingerprint'")
					case "native_meta_missing":
						e = d.storage.syncBackend.Exec(ctx, "ALTER TABLE "+schema.Quote(o.SyncNamespace)+"._ponder_go_meta RENAME TO _ponder_go_meta_saved")
					case "cache_owner":
						e = d.storage.syncBackend.Exec(ctx, "COMMENT ON SCHEMA "+schema.Quote(o.SyncNamespace+"_rpc_1")+" IS 'invalid'")
					case "impossible_intent", "impossible_orphan":
						fault, restore := liveDomainsFault(d, "business", "exec_abort")
						_, e = d.CoordinateReorg(ctx, request)
						restore()
						if e == nil || !fault.fired {
							t.Fatal("pending not staged")
						}
						l, err := d.readLedger(ctx, d.storage.syncBackend)
						if err != nil {
							t.Fatal(err)
						}
						intent := l.Intents[request.Reorg.ID]
						if mode == "impossible_intent" {
							intent.Before.Generation++
						} else {
							intent.Request.Orphaned[0].Hash = fmt.Sprintf("0x%064x", 999999)
							intent.Request.Orphaned[1].ParentHash = intent.Request.Orphaned[0].Hash
						}
						raw, _ := json.Marshal(intent)
						record := l.Records[len(l.Records)-1]
						record.Payload = string(raw)
						record.Hash = ""
						record.Hash = managedHash(record)
						tx, err := d.storage.syncBackend.Begin(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback(ctx)
						if err = tx.Exec(ctx, "UPDATE "+d.table("log")+" SET payload=$1,hash=$2 WHERE sequence=$3", record.Payload, record.Hash, record.Sequence); err != nil {
							t.Fatal(err)
						}
						if err = tx.Exec(ctx, "UPDATE "+d.table("head")+" SET hash=$1 WHERE key='head'", record.Hash); err != nil {
							t.Fatal(err)
						}
						e = tx.Commit(ctx)
					}
					if e != nil {
						t.Fatal(e)
					}
					if mode == "rpc_trigger" {
						if e = d.WithSync(ctx, func(*SourceSyncStore) error { t.Fatal("tampered RPC catalog admitted callback"); return nil }); !errors.Is(e, ErrManagedLiveReceiptConflict) {
							t.Fatalf("RPC trigger gate=%v", e)
						}
					}
					if mode == "missing_business" {
						if e = mutationBusiness.Close(); e != nil {
							t.Fatal(e)
						}
					}
					if e = d.Close(ctx); e != nil {
						t.Fatal(e)
					}
					opts.OpenID = "must-reject-corrupt-peer"
					var wrong *ManagedLiveDomains
					if dialect == "postgres" {
						wrong, e = OpenManagedLiveDomainsPostgres(ctx, o, opts)
					} else {
						wrong, e = OpenManagedLiveDomainsPGlite(ctx, o, opts)
					}
					if e == nil {
						_ = wrong.Close(ctx)
						t.Fatal("corrupt or missing peer adopted")
					}
					probe := liveDomainsRawOpen(t, o, dialect, true)
					if mode == "missing_business" {
						r, e := probe.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.BusinessNamespace)
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("missing business recreated: %v %v", r.Rows, e)
						}
						return
					}
					after, e := m.snapshotHash(ctx, probe, c.LastOperation)
					if e != nil || after != before {
						t.Fatalf("failed cross-domain audit changed prior business snapshot: %s %s %v", before, after, e)
					}
					if mode == "missing_sync" && dialect == "postgres" {
						r, e := probe.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname=$1", o.SyncNamespace)
						if e != nil || len(r.Rows) != 0 {
							t.Fatalf("missing sync recreated: %v %v", r.Rows, e)
						}
					}
					t.Logf("corruption %s rejected before business mutation: %s", mode, fmt.Sprint(e))
				})
			}
		})
	}
}
