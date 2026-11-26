package historical

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// An independent fixture schema raises actual PostgreSQL error responses, one
// while running a statement and another after the application has finished the
// callback and PostgreSQL is executing a deferred constraint trigger at COMMIT.
func retryFaultSchema(t *testing.T, f finiteFixture) string {
	t.Helper()
	ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace + "_fault")
	db, err := store.OpenPostgres(t.Context(), f.options.Owned.Storage.PostgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, sql := range []string{
		"CREATE SCHEMA " + ns,
		"CREATE FUNCTION " + ns + ".conflict() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'statement conflict' USING ERRCODE='40P01'; END $$",
		"CREATE TABLE " + ns + ".faults (id integer)",
		"CREATE FUNCTION " + ns + ".on_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit conflict' USING ERRCODE='40001'; END $$",
		"CREATE CONSTRAINT TRIGGER abort_commit AFTER INSERT ON " + ns + ".faults DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " + ns + ".on_commit()",
		"CREATE FUNCTION " + ns + ".schedule_conflict() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN INSERT INTO " + ns + ".faults VALUES (1); RETURN 1; END $$",
	} {
		if err := db.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	return ns
}

func TestRuntimeHistoricalRetryTransactions(t *testing.T) {
	for _, mode := range []string{"multichain", "omnichain", "isolated", "setup", "cancel_reopen", "pglite"} {
		t.Run(mode, func(t *testing.T) {
			continuousRoot(t)
			ctx := t.Context()
			backend := "postgres"
			if mode == "pglite" {
				backend = "pglite"
			}
			p := newFiniteRPC(t, 5)
			var ns string
			faults := 0
			inject := true
			handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
				var err error
				if mode == "isolated" {
					id := finiteChain(a)
					_, err = a.Context.DB.SQL(ctx, "INSERT INTO account (chain_id,id,balance) VALUES ($1,$2,1) ON CONFLICT (chain_id,id) DO UPDATE SET balance = account.balance+1", id, fmt.Sprintf("chain%d", id))
				} else {
					err = finiteIncrement(ctx, a)
				}
				if err != nil {
					return err
				}
				if !inject || finiteChain(a) != 1 || faults >= 2 {
					return nil
				}
				if err := a.Context.DB.Flush(ctx, "account"); err != nil {
					return err
				}
				faults++
				if mode == "pglite" {
					_, err := a.Context.DB.SQL(ctx, "SELECT 1 / 0")
					return err
				}
				if faults == 1 {
					// A swallowed transaction conflict still aborts this attempt.
					_, err := a.Context.DB.SQL(ctx, "SELECT "+ns+".conflict()")
					if !store.IsPostgresTransactionConflict(err) {
						return fmt.Errorf("expected server transaction conflict, got %v", err)
					}
					return nil
				}
				_, err = a.Context.DB.SQL(ctx, "SELECT "+ns+".schedule_conflict()")
				return err
			})
			var build *config.SourceIndexingBuild
			switch mode {
			case "omnichain":
				build = omnichainRuntimeBuild(t, p, 2, handler)
			case "isolated":
				raw := fmt.Sprintf(`{"ordering":%q,"chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true},"two":{"id":2,"rpc":%q,"reorgWindow":1,"cacheRpcRequests":true}},"blocks":{"Blocks":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":2,"interval":1}}}`, schema.Isolated, p.server.URL+"/1", p.server.URL+"/2")
				build = runtimeSourceBuild(t, raw, config.SourceIndexingFunction{Name: "Blocks:block", Fn: handler})
			case "setup":
				build = setupReorgBuild(t, p, 2, 1, true, finiteIncrement, handler)
			default:
				build = runtimeBuild(t, p, 2, 1, handler)
			}
			f := newManagedFiniteFixture(t, backend, build)
			if mode == "isolated" {
				f.options.Owned.Storage.Ordering = schema.Isolated
				table := &f.options.Owned.Storage.Schema.Tables[0]
				table.Columns[0].Primary = false
				table.Columns = append(table.Columns, schema.Column{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true})
				table.PrimaryKey = []string{"chain_id", "id"}
			}
			if backend == "postgres" {
				ns = retryFaultSchema(t, f)
			}
			var epochs []uint64
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "retry-transactions", OpenID: "first", Requesters: runtimeFactory(p, &epochs), SourceCheckpoints: true}
			var r *RuntimeRunner
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			var delays []time.Duration
			o.Historical.RetryWait = func(ctx context.Context, d time.Duration) error {
				delays = append(delays, d)
				if _, err := r.Control(ctx); err != nil {
					return err
				}
				if !errors.Is(r.Close(ctx), store.ErrManagedLiveBusy) {
					return fmt.Errorf("recursive close was admitted")
				}
				if mode == "cancel_reopen" {
					cancel()
				}
				return ctx.Err()
			}
			maxAttempts := 0
			if mode == "omnichain" || mode == "isolated" {
				o.Historical.OnCommitted = func(_ context.Context, group GroupResult) error {
					maxAttempts = max(maxAttempts, group.Attempts)
					return nil
				}
			}
			r = runtimeOpen(t, backend, o)
			out, err := r.Run(runCtx)
			if mode == "pglite" || mode == "cancel_reopen" {
				if err == nil || faults != 1 || out.Completed {
					t.Fatalf("failed callback: %+v faults=%d err=%v", out, faults, err)
				}
				if mode == "cancel_reopen" && (!errors.Is(err, context.Canceled) || !reflect.DeepEqual(delays, []time.Duration{125 * time.Millisecond})) {
					t.Fatalf("canceled wait: %v %v", delays, err)
				}
				if mode == "pglite" && len(delays) != 0 {
					t.Fatalf("PGlite waited: %v", delays)
				}
				if err := r.Close(ctx); err != nil {
					t.Fatal(err)
				}
				inject = false
				o.OpenID = "resume"
				r = runtimeOpen(t, backend, o)
				out, err = r.Run(ctx)
			} else if !reflect.DeepEqual(delays, []time.Duration{125 * time.Millisecond, 250 * time.Millisecond}) || faults != 2 {
				t.Fatalf("transaction retry: waits=%v faults=%d run=%+v err=%v", delays, faults, out, err)
			}
			if err != nil || !out.Completed || !out.Ready || ((mode == "omnichain" || mode == "isolated") && maxAttempts != 3) {
				t.Fatalf("completion: %+v attempts=%d err=%v", out, maxAttempts, err)
			}
			if err := r.Close(ctx); err != nil {
				t.Fatal(err)
			}
			wantBalance := "2"
			if mode == "setup" {
				wantBalance = "3"
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || rows[0]["id"] != "chain1" || rows[1]["id"] != "chain2" || rows[0]["balance"] != wantBalance || rows[1]["balance"] != wantBalance {
					t.Fatalf("persisted rows: %+v", rows)
				}
			})
		})
	}
}

func TestRuntimeHistoricalRetryTerminal(t *testing.T) {
	continuousRoot(t)
	p := newFiniteRPC(t, 5)
	terminal := &indexing.DatabaseError{Name: "Terminal", Cause: &pgconn.PgError{Code: "40001", Message: "explicit permanent error"}}
	build := runtimeBuild(t, p, 2, 1, func(context.Context, *indexing.HandlerArgs) error { return terminal })
	f := newManagedFiniteFixture(t, "postgres", build)
	var epochs []uint64
	o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "terminal", OpenID: "first", Requesters: runtimeFactory(p, &epochs)}
	waits := 0
	o.Historical.RetryWait = func(context.Context, time.Duration) error { waits++; return nil }
	r := runtimeOpen(t, "postgres", o)
	_, err := r.Run(t.Context())
	if !errors.Is(err, terminal) || waits != 0 {
		t.Fatalf("waits=%d err=%v", waits, err)
	}
}

func TestRuntimeHistoricalRetryBudget(t *testing.T) {
	first := &pgconn.PgError{Code: "40001", Message: "first"}
	later := &pgconn.PgError{Code: "40P01", Message: "later"}
	attempts := 0
	var delays []time.Duration
	err := retryTransaction(t.Context(), func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}, store.IsPostgresTransactionConflict, func() error {
		attempts++
		if attempts == 1 {
			return first
		}
		return later
	})
	if err != first || attempts != 10 || len(delays) != 9 {
		t.Fatalf("attempts=%d delays=%v error=%v", attempts, delays, err)
	}
	for i, d := range delays {
		if d != (125*time.Millisecond)<<i {
			t.Fatalf("delay %d: %v", i, d)
		}
	}
	for _, terminal := range []error{
		context.Canceled, errors.New("serialization failure (SQLSTATE 40001)"), &pgconn.PgError{Code: "08006"},
		&store.ManagedStageError{Stage: "live-batch", Outcome: "unknown", Err: first},
		errors.Join(first, context.DeadlineExceeded),
	} {
		if err := retryTransaction(t.Context(), func(context.Context, time.Duration) error {
			t.Fatal("wait called for terminal failure")
			return nil
		}, store.IsPostgresTransactionConflict, func() error { return terminal }); err != terminal {
			t.Fatalf("terminal error changed: %v", err)
		}
	}
}
