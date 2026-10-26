package historical

// Supplemental managed-finite integration coverage. These tests add no
// upstream-original test credit. Both backends use real persistent storage;
// raw catalog inspection opens PGlite only after the managed owner has closed.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

// Ordinary cases are strictly serial. A fresh pair is shared only within this
// package test process; later native batches allocate a new pair, never an old
// path. A failed case poisons reuse rather than assuming its child has exited.
var managedHistoricalShared struct {
	once            sync.Once
	root, directory string
	err             error
	poisoned        atomic.Bool
}

func managedHistoricalDirectory(t *testing.T, root string) string {
	t.Helper()
	if strings.HasPrefix(t.Name(), "TestManagedFiniteCloseRetainsOwnership/") {
		directory, err := os.MkdirTemp(root, "historical-managed-dedicated-")
		if err != nil {
			t.Fatal(err)
		}
		return directory
	}
	managedHistoricalShared.once.Do(func() {
		managedHistoricalShared.root = root
		managedHistoricalShared.directory, managedHistoricalShared.err = os.MkdirTemp(root, "historical-managed-shared-")
	})
	if managedHistoricalShared.err != nil {
		t.Fatal(managedHistoricalShared.err)
	}
	if managedHistoricalShared.root != root || managedHistoricalShared.poisoned.Load() {
		t.Fatal("shared managed fixture root changed or prior case failed; refusing directory reuse")
	}
	t.Cleanup(func() {
		if t.Failed() {
			managedHistoricalShared.poisoned.Store(true)
		}
	})
	return managedHistoricalShared.directory
}

// Unlike the legacy fixture, M44 keeps all task-owned schemas and persistent
// directories as evidence. The bounded harness owns retention and disk budget.
func newManagedFiniteFixture(t *testing.T, backend string, build *config.SourceIndexingBuild) finiteFixture {
	t.Helper()
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	options := Options{Build: build, QuerySize: 2, Clock: func() time.Time { return time.Unix(2000, 0) }, Owned: indexing.OwnedOptions{Storage: store.IndependentStorageOptions{BusinessNamespace: "ponder_go_m44_b_" + id, SyncNamespace: "ponder_go_m44_s_" + id, Schema: schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}}, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}
	f := finiteFixture{backend: backend, options: options, open: OpenPGlite}
	if backend == "postgres" {
		f.open = OpenPostgres
		f.options.Owned.Storage.PostgresURL = os.Getenv("PONDER_TEST_POSTGRES")
		if f.options.Owned.Storage.PostgresURL == "" {
			t.Fatal("explicit task PostgreSQL required; no skip")
		}
	} else {
		root := os.Getenv("PONDER_M44_TEST_ROOT")
		if root == "" {
			t.Fatal("PONDER_M44_TEST_ROOT required for retained PGlite evidence")
		}
		dir := managedHistoricalDirectory(t, root)
		f.options.Owned.Storage.BusinessDirectory = filepath.Join(dir, "business")
		f.options.Owned.Storage.SyncDirectory = filepath.Join(dir, "sync")
		t.Logf("retained M44 PGlite directory: %s", dir)
	}
	// Both physical domains are closed before returning this preflight. No raw
	// PGlite observer overlaps an active managed or legacy process.
	inspect := func(b store.Backend) {
		for _, namespace := range []string{options.Owned.Storage.BusinessNamespace, options.Owned.Storage.SyncNamespace} {
			for _, suffix := range []string{"_rpc_1", "_legacy", "_sync", "_v", ""} {
				name := namespace + suffix
				if rows := managedRows(t, b, "SELECT nspname FROM pg_namespace WHERE nspname=$1", name); len(rows) != 0 {
					t.Fatalf("pre-existing M44 namespace: %s", name)
				}
			}
		}
	}
	managedInspect(t, f, inspect)
	if backend == "pglite" {
		syncFixture := f
		syncFixture.options.Owned.Storage.BusinessDirectory = f.options.Owned.Storage.SyncDirectory
		managedInspect(t, syncFixture, inspect)
	}
	t.Logf("retained M44 case=%s backend=%s business=%s sync=%s business_dir=%s sync_dir=%s", t.Name(), backend, options.Owned.Storage.BusinessNamespace, options.Owned.Storage.SyncNamespace, f.options.Owned.Storage.BusinessDirectory, f.options.Owned.Storage.SyncDirectory)

	return f
}

func managedOpen(f finiteFixture, ctx context.Context, options ManagedOptions) (*ManagedRunner, error) {
	if f.backend == "postgres" {
		return OpenManagedPostgres(ctx, options)
	}
	return OpenManagedPGlite(ctx, options)
}

func managedStart(t *testing.T, f finiteFixture, options ManagedOptions) (*ManagedRunner, *bool) {
	t.Helper()
	r, err := managedOpen(f, context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	actuallyCanceled := new(bool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := r.Close(ctx); err != nil && !(f.backend == "postgres" && *actuallyCanceled && managedCanceledCloseOnly(err)) {
			t.Error(err)
		}
	})
	return r, actuallyCanceled
}

func managedClose(t *testing.T, r *ManagedRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func managedInspect(t *testing.T, f finiteFixture, fn func(store.Backend)) {
	t.Helper()
	var b store.Backend
	var err error
	if f.backend == "postgres" {
		b, err = store.OpenPostgres(context.Background(), f.options.Owned.Storage.PostgresURL)
	} else {
		b, err = store.OpenPGlite(context.Background(), f.options.Owned.Storage.BusinessDirectory)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Fatal("raw fixture observer did not close; refusing reuse", err)
		}
	}()
	fn(b)
}

func managedRows(t *testing.T, b store.Backend, query string, args ...any) []map[string]any {
	t.Helper()
	r, err := b.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return r.Rows
}

func managedCatalog(t *testing.T, f finiteFixture, alias string, active bool) {
	t.Helper()
	managedInspect(t, f, func(b store.Backend) {
		ns := f.options.Owned.Storage.BusinessNamespace
		indexes := managedRows(t, b, "SELECT indexname FROM pg_indexes WHERE schemaname=$1 AND indexname=$2", ns, "account_balance_idx")
		if (len(indexes) == 1) != active {
			t.Fatalf("secondary index activation=%v, rows=%v", active, indexes)
		}
		triggers := managedRows(t, b, "SELECT t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='account' AND t.tgname='live_query' AND NOT t.tgisinternal", ns)
		if (len(triggers) == 1) != active {
			t.Fatalf("live-query activation=%v, rows=%v", active, triggers)
		}
		if alias != "" {
			views := managedRows(t, b, "SELECT table_name FROM information_schema.views WHERE table_schema=$1 AND table_name='account'", alias)
			if (len(views) == 1) != active {
				t.Fatalf("alias activation=%v, rows=%v", active, views)
			}
		}
	})
}

func managedFixtureOptions(t *testing.T, f *finiteFixture, aliases bool) ManagedOptions {
	t.Helper()
	f.options.Owned.Storage.Schema.Tables[0].Indexes = []schema.Index{{Name: "account_balance_idx", Columns: []string{"balance"}}}
	o := ManagedOptions{Historical: f.options, BuildID: "managed-finite-test-v1"}
	if aliases {
		o.ViewsSchema = f.options.Owned.Storage.BusinessNamespace + "_v"
		{
			alias := o.ViewsSchema
			managedInspect(t, *f, func(b store.Backend) {
				if rows := managedRows(t, b, "SELECT nspname FROM pg_namespace WHERE nspname=$1", alias); len(rows) != 0 {
					t.Fatalf("pre-existing alias namespace, no cleanup authorized: %s", alias)
				}
			})
		}
	}
	return o
}

func TestManagedFiniteReadyAndResume(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, aliases := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/aliases=%v", backend, aliases), func(t *testing.T) {
				p := newFiniteRPC(t, 3)
				var handlers, setups atomic.Int64
				handler := indexing.Handler(func(ctx context.Context, args *indexing.HandlerArgs) error {
					handlers.Add(1)
					return finiteIncrement(ctx, args)
				})
				setup := indexing.Handler(func(context.Context, *indexing.HandlerArgs) error { setups.Add(1); return nil })
				build := finiteBuild(t, p, 3, true, handler, setup)
				f := newManagedFiniteFixture(t, backend, build)
				o := managedFixtureOptions(t, &f, aliases)
				r, _ := managedStart(t, f, o)
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatalf("new managed owner ready=%v: %v", ready, err)
				}
				managedClose(t, r)
				managedCatalog(t, f, o.ViewsSchema, false)

				r, _ = managedStart(t, f, o)
				result, err := r.Run(context.Background())
				if err != nil || !result.Historical.Completed || !result.Ready || result.Historical.Resumed {
					t.Fatalf("initial managed run=%+v: %v", result, err)
				}
				if ready, err := r.Ready(context.Background()); err != nil || !ready {
					t.Fatalf("durable ready=%v: %v", ready, err)
				}
				if _, err := r.Run(context.Background()); err == nil {
					t.Fatal("managed owner permitted a second run")
				}
				if handlers.Load() != 6 || setups.Load() != 2 {
					t.Fatalf("unexpected handlers/setup: %d/%d", handlers.Load(), setups.Load())
				}
				managedClose(t, r)
				managedCatalog(t, f, o.ViewsSchema, true)
				managedInspect(t, f, func(b store.Backend) {
					ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
					rows := managedRows(t, b, "SELECT id,balance::text AS balance FROM "+ns+".account ORDER BY id")
					want := []map[string]any{{"id": "chain1", "balance": "3"}, {"id": "chain2", "balance": "3"}}
					if !reflect.DeepEqual(rows, want) {
						t.Fatalf("durable rows: %v", rows)
					}
					states := managedRows(t, b, "SELECT c.chain_id::text AS chain_id,p.checkpoint AS latest,f.checkpoint AS finalized,f.safe_checkpoint AS safe,r.checkpoint AS cursor FROM "+ns+"._ponder_go_chains c JOIN "+ns+"._ponder_go_progress p USING(chain_id) JOIN "+ns+"._ponder_go_finalized f USING(chain_id) JOIN "+ns+"._ponder_go_runtime_cursor r USING(chain_id)")
					if len(states) != 2 {
						t.Fatal(states)
					}
					for _, state := range states {
						cursor, ok := state["cursor"].(string)
						cp, err := checkpoint.Decode(cursor)
						if !ok || err != nil || cp.BlockNumber != 3 || state["latest"] != cursor || state["safe"] != cursor || state["finalized"] != cursor {
							t.Fatalf("inexact ready boundary: %v, %v", state, err)
						}
					}
					if rows := managedRows(t, b, "SELECT 1 FROM "+ns+"._ponder_go_journal LIMIT 1"); len(rows) != 0 {
						t.Fatal("safe completed history retained an unsupported journal", rows)
					}
				})

				r, _ = managedStart(t, f, o)
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatalf("reopen did not reset readiness: %v %v", ready, err)
				}
				resumed, err := r.Run(context.Background())
				if err != nil || !resumed.Historical.Resumed || !resumed.Historical.Completed || !resumed.Ready || len(resumed.Historical.Groups) != 0 {
					t.Fatalf("resumed=%+v: %v", resumed, err)
				}
				if handlers.Load() != 6 || setups.Load() != 2 {
					t.Fatalf("reopen duplicated handler/setup: %d/%d", handlers.Load(), setups.Load())
				}
				managedClose(t, r)
			})
		}
	}
}

func TestManagedFiniteRejectsUnmanagedAndIdentityChanges(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			p := newFiniteRPC(t, 3)
			build := finiteBuild(t, p, 3, false, indexing.Handler(finiteIncrement), nil)
			legacy := newManagedFiniteFixture(t, backend, build)
			old := legacy.start(t)
			if err := old.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if r, err := managedOpen(legacy, context.Background(), ManagedOptions{Historical: legacy.options, BuildID: "managed-finite-test-v1"}); err == nil {
				_ = r.Close(context.Background())
				t.Fatal("managed constructor adopted an unmanaged namespace")
			} else if !strings.Contains(err.Error(), "existing namespace is not managed finite; use a new namespace") {
				t.Fatal("legacy rejection was not namespace-format rejection", err)
			}

			f := newManagedFiniteFixture(t, backend, build)
			o := managedFixtureOptions(t, &f, false)
			r, _ := managedStart(t, f, o)
			managedClose(t, r)
			changedBuild := o
			changedBuild.BuildID = "different-handler-version"
			if r, err := managedOpen(f, context.Background(), changedBuild); err == nil {
				_ = r.Close(context.Background())
				t.Fatal("accepted changed build identity")
			}
			changedRange := o
			changedRange.Historical.Build = finiteBuild(t, p, 2, false, indexing.Handler(finiteIncrement), nil)
			if r, err := managedOpen(f, context.Background(), changedRange); err == nil {
				_ = r.Close(context.Background())
				t.Fatal("accepted changed range with unchanged BuildID")
			}
			// Rejected opens must leave the original, still-empty owner resumable.
			r, _ = managedStart(t, f, o)
			if ready, err := r.Ready(context.Background()); err != nil || ready {
				t.Fatal(ready, err)
			}
			managedClose(t, r)
		})
	}
}

func TestManagedFiniteFailureNeverReady(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, failure := range []string{"handler", "caller-canceled-before-run"} {
			t.Run(backend+"/"+failure, func(t *testing.T) {
				marker := &store.NonRetryableUserError{Message: "managed controlled handler failure"}
				handler := indexing.Handler(finiteIncrement)
				if failure == "handler" {
					handler = func(context.Context, *indexing.HandlerArgs) error { return marker }
				}
				p := newFiniteRPC(t, 2)
				f := newManagedFiniteFixture(t, backend, finiteBuild(t, p, 2, false, handler, nil))
				o := managedFixtureOptions(t, &f, true)
				r, _ := managedStart(t, f, o)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if failure == "caller-canceled-before-run" {
					cancel()
				}
				result, err := r.Run(ctx)
				if err == nil || result.Ready || (failure == "handler" && !errors.Is(err, marker)) || (failure != "handler" && !errors.Is(err, context.Canceled)) {
					t.Fatalf("failure became ready or changed error: %+v %v", result, err)
				}
				if ready, err := r.Ready(context.Background()); err != nil || ready {
					t.Fatal(ready, err)
				}
				managedClose(t, r)
				managedCatalog(t, f, o.ViewsSchema, false)
			})
		}
	}
}

func TestManagedFiniteCloseRetainsOwnership(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			handler := indexing.Handler(func(ctx context.Context, _ *indexing.HandlerArgs) error {
				enterOnce.Do(func() { close(entered) })
				<-release
				return ctx.Err()
			})
			p := newFiniteRPC(t, 2)
			build := finiteBuild(t, p, 2, false, handler, nil)
			f := newManagedFiniteFixture(t, backend, build)
			o := managedFixtureOptions(t, &f, false)
			r, canceledClose := managedStart(t, f, o)
			finished := make(chan error, 1)
			go func() { _, err := r.Run(context.Background()); finished <- err }()
			finiteWait(t, entered, "managed handler entered its actual transaction")
			deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			err := r.Close(deadline)
			cancel()
			*canceledClose = true // This exact owner Close has actually been invoked.
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close did not retain timed-out shutdown: %v", err)
			}
			other := o
			other.Historical.Build = finiteBuild(t, p, 2, false, handler, nil)
			contend, stopContend := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopContend()
			if replacement, err := managedOpen(f, contend, other); err == nil {
				_ = replacement.Close(context.Background())
				t.Fatal("replacement entered while original handler still owned storage")
			}
			unblock()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Close did not cancel actual handler scope: %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("managed Run did not join after handler release")
			}
			join, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			if err := r.Close(join); err != nil && !(backend == "postgres" && managedCanceledCloseOnly(err)) {
				t.Fatal(err)
			}
			// Only now may the actual storage/process exclusion be released.
			reopened, _ := managedStart(t, f, other)
			if ready, err := reopened.Ready(context.Background()); err != nil || ready {
				t.Fatal(ready, err)
			}
			managedClose(t, reopened)
		})
	}
}

// Only use after an actual owner Close canceled its fixed PostgreSQL session.
// ErrLeaseLost is then an expected additional managed owner-fence leaf, not a
// general cleanup exemption. All unrelated and wrapped unknown leaves fail.
func managedCanceledCloseOnly(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		leaves := joined.Unwrap()
		if len(leaves) == 0 {
			return false
		}
		for _, leaf := range leaves {
			if !managedCanceledCloseOnly(leaf) {
				return false
			}
		}
		return true
	}
	return err == store.ErrLeaseLost || err == sql.ErrConnDone || err == driver.ErrBadConn
}
func TestManagedCanceledCloseErrorClassification(t *testing.T) {
	expected := errors.Join(store.ErrLeaseLost, sql.ErrConnDone, driver.ErrBadConn)
	if !managedCanceledCloseOnly(expected) {
		t.Fatal("known cancellation leaves rejected")
	}
	for _, err := range []error{nil, errors.Join(expected, errors.New("unrelated")), fmt.Errorf("unexpected wrapper: %w", store.ErrLeaseLost)} {
		if managedCanceledCloseOnly(err) {
			t.Fatalf("unsafe cleanup allowance: %v", err)
		}
	}
}

// A real callback Client escapes with a separate caller lifetime. Its HTTP I/O
// is already admitted before the callback returns; activation must join it.
// The cancellation branch is an actual activation-wait error, not an assertion
// of equivalence to a failed cache write or prefetch persistence operation.
func TestManagedFiniteEscapedClientActivationBarrier(t *testing.T) {
	for _, backend := range []string{"postgres", "pglite"} {
		for _, cancelActivation := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel_activation=%v", backend, cancelActivation), func(t *testing.T) {
				p := newFiniteRPC(t, 2)
				entered, release := make(chan struct{}), make(chan struct{})
				var enterOnce, releaseOnce, launchOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				defer unblock()
				p.hook = func(ctx context.Context, call finiteRPCRequest) error {
					if call.Method == "eth_getBalance" && len(call.Params) > 0 && call.Params[0] == "0xheld" {
						enterOnce.Do(func() { close(entered) })
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				}
				escapedDone := make(chan error, 1)
				var client *indexing.Client
				handler := indexing.Handler(func(ctx context.Context, args *indexing.HandlerArgs) error {
					launchOnce.Do(func() {
						client = args.Context.Client
						go func() {
							value, err := client.GetBalance(context.Background(), "0xheld", indexing.BalanceOptions{BlockNumber: big.NewInt(1)})
							if err == nil && value.Int64() != 42 {
								err = errors.New("escaped request returned wrong balance")
							}
							escapedDone <- err
						}()
					})
					select {
					case <-entered:
					case <-ctx.Done():
						return ctx.Err()
					}
					return finiteIncrement(ctx, args)
				})
				f := newManagedFiniteFixture(t, backend, finiteBuild(t, p, 2, false, handler, nil))
				options := managedFixtureOptions(t, &f, true)
				runner, _ := managedStart(t, f, options)
				type outcome struct {
					result ManagedResult
					err    error
				}
				done := make(chan outcome, 1)
				runContext, cancelRun := context.WithCancel(context.Background())
				defer cancelRun()
				go func() { result, err := runner.Run(runContext); done <- outcome{result, err} }()
				finiteWait(t, entered, "escaped real callback RPC admission")
				// The existing runner atomic is the real outer historical Run lifetime,
				// not a fabricated Completed value. Poll only this observable lifecycle.
				timer := time.NewTimer(15 * time.Second)
				defer timer.Stop()
				tick := time.NewTicker(time.Millisecond)
				defer tick.Stop()
				for runner.runner.active.Load() {
					select {
					case result := <-done:
						t.Fatalf("managed Run escaped held I/O: %+v %v", result.result, result.err)
					case <-timer.C:
						t.Fatal("historical Run did not finish independently of escaped I/O")
					case <-tick.C:
					}
				}
				if ready, err := runner.Ready(context.Background()); err != nil || ready {
					t.Fatal("ready before escaped client joined", ready, err)
				}
				// Activation follows outer Run return. Observe the exact client seal by
				// harmless real requests; none shares the blocked request's admission token.
				sealed := false
				for !sealed {
					probe, stop := context.WithTimeout(context.Background(), time.Second)
					_, err := client.GetBalance(probe, "0xprobe", indexing.BalanceOptions{BlockNumber: big.NewInt(1)})
					stop()
					if err != nil {
						if !strings.Contains(err.Error(), "managed client admission is sealed") {
							t.Fatal("unexpected escaped-client rejection", err)
						}
						sealed = true
					} else {
						select {
						case <-timer.C:
							t.Fatal("activation never sealed escaped client")
						case <-tick.C:
						}
					}
				}
				select {
				case result := <-done:
					t.Fatalf("activation returned with admitted I/O outstanding: %+v %v", result.result, result.err)
				default:
				}
				if cancelActivation {
					cancelRun()
				}
				unblock()
				select {
				case err := <-escapedDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(15 * time.Second):
					t.Fatal("escaped RPC failed to join")
				}
				select {
				case result := <-done:
					if !result.result.Historical.Completed || (cancelActivation && (!errors.Is(result.err, context.Canceled) || result.result.Ready)) || (!cancelActivation && (result.err != nil || !result.result.Ready)) {
						t.Fatal(result.result, result.err)
					}
				case <-time.After(15 * time.Second):
					t.Fatal("activation failed to join escaped RPC")
				}
				if ready, err := runner.Ready(context.Background()); err != nil || ready == cancelActivation {
					t.Fatal(ready, err)
				}
				managedClose(t, runner)
				managedCatalog(t, f, options.ViewsSchema, !cancelActivation)
			})
		}
	}
}
