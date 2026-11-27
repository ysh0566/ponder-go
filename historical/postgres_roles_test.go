package historical

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Each invocation owns a fresh cluster. Only the preparation runs with database
// CREATE. Reopen, fork recovery and activation use the handed-off writer login.
func restrictManagedPostgres(t *testing.T, ctx context.Context, o *RuntimeOptions, prepare func(*RuntimeRunner)) store.Backend {
	t.Helper()
	instance := testpostgres.Start(t)
	connect := func(uri string) store.Backend {
		t.Helper()
		b, err := store.OpenPostgres(ctx, uri)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		return b
	}
	exec := func(db store.Backend, stmt string) {
		t.Helper()
		if err := db.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	denied := func(db store.Backend, stmt string) {
		t.Helper()
		var pg *pgconn.PgError
		if err := db.Exec(ctx, stmt); !errors.As(err, &pg) || pg.Code != "42501" {
			t.Fatalf("expected insufficient_privilege for %s: %v", stmt, err)
		}
	}
	admin := connect(instance.URL())
	exec(admin, "REVOKE ALL ON DATABASE postgres FROM PUBLIC")
	exec(admin, "REVOKE ALL ON SCHEMA public FROM PUBLIC")
	login := func(name string) string {
		password := rand.Text()
		exec(admin, "CREATE ROLE "+name+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '"+password+"'")
		exec(admin, "GRANT CONNECT ON DATABASE postgres TO "+name)
		u, err := url.Parse(instance.URL())
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(name, password)
		return u.String()
	}
	managerURL, writerURL := login("deploy_manager"), login("index_writer")
	o.Historical.Owned.Storage.PostgresURL = writerURL
	o.Historical.Owned.Storage.BusinessNamespace = "ponder_go_blocks"
	o.Historical.Owned.Storage.SyncNamespace = "ponder_go_blocks_cache"
	manager, writer := connect(managerURL), connect(writerURL)
	exec(admin, "CREATE ROLE index_cache_owner NOLOGIN")
	exec(admin, "GRANT index_writer, index_cache_owner TO deploy_manager")
	exec(admin, "GRANT CREATE ON DATABASE postgres TO deploy_manager")
	exec(admin, "GRANT TEMPORARY ON DATABASE postgres TO index_writer")
	exec(admin, "CREATE SCHEMA unrelated")
	exec(admin, "CREATE TABLE unrelated.other (id int)")
	denied(writer, "CREATE SCHEMA unprovisioned")
	denied(manager, "DROP TABLE unrelated.other")
	exec(admin, "GRANT CREATE ON DATABASE postgres TO index_writer")
	r := runtimeOpen(t, "postgres", *o)
	if prepare != nil {
		prepare(r)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	exec(admin, "REVOKE CREATE ON DATABASE postgres FROM index_writer")
	sql, err := os.ReadFile("../docs/examples/postgres-managed-writer.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(manager, string(sql))
	denied(writer, "CREATE SCHEMA another_deployment")
	denied(writer, "DROP SCHEMA ponder_go_blocks CASCADE")
	denied(writer, "CREATE TABLE ponder_go_blocks_cache.unexpected(id int)")
	denied(writer, "CREATE TABLE ponder_go_blocks_cache_rpc_1.unexpected(id int)")
	denied(writer, "ALTER TABLE ponder_go_blocks_cache_rpc_1.blocks ADD COLUMN unexpected int")
	denied(writer, "DELETE FROM ponder_go_blocks_cache._ponder_go_domains_log WHERE false")
	denied(writer, "UPDATE ponder_go_blocks_cache._ponder_go_meta SET value='wrong' WHERE false")
	denied(writer, "SELECT * FROM unrelated.other")
	denied(writer, "SET ROLE deploy_manager")
	denied(writer, "SET ROLE index_cache_owner")
	denied(writer, "UPDATE ponder_go_blocks_cache._ponder_go_domains_head SET spec='other' WHERE false")
	denied(writer, "UPDATE ponder_go_blocks_cache_rpc_1.blocks SET number=0 WHERE false")
	denied(writer, "DELETE FROM ponder_go_blocks_cache_rpc_1.factories WHERE false")
	exec(admin, "REVOKE CREATE ON DATABASE postgres FROM deploy_manager")

	// A missing grant must stop the pre-open audit before it advances the ledger.
	queryHead := func() any {
		rows, err := manager.Query(ctx, "SELECT sequence::text AS n FROM ponder_go_blocks_cache._ponder_go_domains_head WHERE key='head'")
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("sync ledger head: %v %v", rows, err)
		}
		return rows.Rows[0]["n"]
	}
	before := queryHead()
	exec(manager, "REVOKE SELECT ON ponder_go_blocks_cache._ponder_go_domains_log FROM index_writer")
	o.OpenID = "missing-grant"
	bad, err := OpenRuntimePostgres(ctx, *o)
	var pg *pgconn.PgError
	if bad != nil || !errors.As(err, &pg) || pg.Code != "42501" || before != queryHead() {
		t.Fatalf("reopen without ledger access: %v", err)
	}
	exec(manager, "GRANT SELECT ON ponder_go_blocks_cache._ponder_go_domains_log TO index_writer")
	return manager
}

func TestRuntimePostgresDeploymentRoles(t *testing.T) {
	t.Run("multichain_live", testRestrictedLive)
	for _, order := range []schema.Ordering{schema.Isolated, schema.Omnichain} {
		t.Run(string(order)+"_finalized", func(t *testing.T) { testRestrictedFinalized(t, order) })
	}
}

func testRestrictedLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	node := newContinuousRPC(t, 2)
	handler := indexing.Handler(func(ctx context.Context, args *indexing.HandlerArgs) error {
		if _, err := args.Context.Client.GetBalance(ctx, "0x"+strings.Repeat("1", 40)); err != nil {
			return err
		}
		return finiteIncrement(ctx, args)
	})
	build := runtimeBuild(t, node.p, 5, 1, handler)
	for _, c := range build.Executable().Chains {
		c["reorgWindow"] = uint64(600)
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{
		{Name: "id", Type: "text", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true},
	}, Indexes: []schema.Index{{Name: "balance_index", Columns: []string{"balance"}}}}}}
	epochs := []uint64{}
	o := RuntimeOptions{OpenID: "prepare", BuildID: "role-acceptance", ViewsSchema: "ponder_go_blocks_views",
		Requesters: runtimeFactory(node.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond},
		Historical: Options{Build: build, Owned: indexing.OwnedOptions{Storage: store.IndependentStorageOptions{
			Schema: model,
		}, Service: indexing.Options{Errors: &indexing.ErrorState{}}}},
	}
	var r *RuntimeRunner
	reached := func(n uint64) bool {
		state, err := r.Control(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []uint64{1, 2} {
			cp, err := checkpoint.Decode(state.Cursors[id].Checkpoint)
			if err != nil || cp.BlockNumber < n {
				return false
			}
		}
		return true
	}
	stop := errors.New("prepared deployment")
	o.Live.OnProgress = func(ctx context.Context, p RuntimeLiveProgress) error {
		if p.Type == "block" && reached(2) {
			return stop
		}
		return nil
	}
	manager := restrictManagedPostgres(t, ctx, &o, func(preparing *RuntimeRunner) {
		r = preparing
		if _, err := r.Run(ctx); !errors.Is(err, stop) {
			t.Fatalf("prepare: %v", err)
		}
	})
	node.setLatest(3)
	var forked, rolledBack bool
	o.OpenID = "restricted"
	o.Live.OnProgress = func(ctx context.Context, p RuntimeLiveProgress) error {
		if !forked && p.Type == "block" && reached(3) {
			forked = true
			node.fork(1, 3)
			node.setLatest(5)
		}
		if p.Type == "reorg" {
			rolledBack = true
		}
		return nil
	}
	r = runtimeOpen(t, "postgres", o)
	out, err := r.Run(ctx)
	if err != nil || !forked || !rolledBack || !out.Ready || !out.Completed || !out.Recovered {
		t.Fatalf("restricted run: %+v fork=%v reorg=%v err=%v", out, forked, rolledBack, err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	o.OpenID = "restart"
	o.Live.OnProgress = nil
	r = runtimeOpen(t, "postgres", o)
	out, err = r.Run(ctx)
	if err != nil || !out.Recovered || !out.Ready || !out.Completed {
		t.Fatalf("cold restart: %+v %v", out, err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := manager.Query(ctx, "SELECT id,balance::text AS balance FROM ponder_go_blocks_views.account ORDER BY id")
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("published rows: %v %v", rows, err)
	}
	for i, row := range rows.Rows {
		if row["id"] != fmt.Sprintf("chain%d", i+1) || row["balance"] != "5" {
			t.Fatalf("published row after replay: %v", row)
		}
	}
}
func testRestrictedFinalized(t *testing.T, order schema.Ordering) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	p := newFiniteRPC(t, 6)
	raw := fmt.Sprintf(`{"ordering":%q,"chains":{"one":{"id":1,"rpc":%q,"reorgWindow":1},"two":{"id":2,"rpc":%q,"reorgWindow":1}},"blocks":{"Head":{"chain":{"one":{},"two":{}},"startBlock":1,"endBlock":3,"interval":1}}}`, order, p.server.URL+"/1", p.server.URL+"/2")
	handler := indexing.Handler(func(ctx context.Context, a *indexing.HandlerArgs) error {
		n, err := continuousNumber(a)
		if err != nil {
			return err
		}
		_, err = a.Context.DB.InsertOne(ctx, "seen", store.Row{"chainId": int(finiteChain(a)), "height": int(n)}, store.Conflict{})
		return err
	})
	build := runtimeSourceBuild(t, raw, config.SourceIndexingFunction{Name: "Head:block", Fn: handler})
	for _, chain := range build.Executable().EventCallbacks {
		for _, cb := range chain {
			f := cb["filter"].(map[string]any)
			f["include"] = indexing.RequiredFilterInclude("block", false)
		}
	}
	model := schema.Schema{Tables: []schema.Table{{Name: "seen", PrimaryKey: []string{"chain_id", "height"}, Columns: []schema.Column{{Name: "chain_id", Property: "chainId", Type: "integer", NotNull: true}, {Name: "height", Type: "integer", NotNull: true}}}}}
	o := RuntimeOptions{OpenID: "initialize", BuildID: "finalized-role-" + string(order), Historical: Options{Build: build, Owned: indexing.OwnedOptions{Storage: store.IndependentStorageOptions{Schema: model, Ordering: order}, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}}
	manager := restrictManagedPostgres(t, ctx, &o, nil)
	for _, opening := range []string{"writer", "reopen"} {
		o.OpenID = opening
		r := runtimeOpen(t, "postgres", o)
		out, err := r.Run(ctx)
		if err != nil || !out.Ready || !out.Completed || (opening == "reopen" && out.HistoricalBlocks != 0) {
			t.Fatalf("%s: %+v %v", opening, out, err)
		}
		if err := r.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := manager.Query(ctx, "SELECT chain_id::text AS chain,height::text AS height FROM ponder_go_blocks.seen ORDER BY chain_id,height")
	if err != nil || len(rows.Rows) != 6 {
		t.Fatalf("finalized rows: %v %v", rows, err)
	}
	for i, row := range rows.Rows {
		if row["chain"] != fmt.Sprint(1+i/3) || row["height"] != fmt.Sprint(1+i%3) {
			t.Fatalf("row after cold reopen: %v", row)
		}
	}
}
