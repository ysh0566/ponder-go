package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Exercise actual login privileges on a disposable cluster with PostgreSQL's
// default PUBLIC database and public-schema grants explicitly removed.
func TestGeneratedDeploymentRoles(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	instance := testpostgres.Start(t)
	connect := func(address string) store.Backend {
		t.Helper()
		b, err := store.OpenPostgres(ctx, address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		return b
	}
	exec := func(b store.Backend, sql string) {
		t.Helper()
		if err := b.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	denied := func(err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("expected insufficient_privilege, got %v", err)
		}
	}
	admin := connect(instance.URL())
	exec(admin, "REVOKE ALL ON DATABASE postgres FROM PUBLIC")
	exec(admin, "REVOKE ALL ON SCHEMA public FROM PUBLIC")
	login := func(role string) string {
		t.Helper()
		password := make([]byte, 24)
		if _, err := rand.Read(password); err != nil {
			t.Fatal(err)
		}
		secret := hex.EncodeToString(password)
		exec(admin, "CREATE ROLE "+role+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD '"+secret+"'")
		exec(admin, "GRANT CONNECT ON DATABASE postgres TO "+role)
		u, err := url.Parse(instance.URL())
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(role, secret)
		return u.String()
	}
	managerURL := login("deploy_manager")
	writerURL := login("index_writer")
	manager, writer := connect(managerURL), connect(writerURL)
	denied(manager.Exec(ctx, "CREATE SCHEMA not_provisioned"))
	exec(admin, "GRANT CREATE ON DATABASE postgres TO deploy_manager")
	denied(writer.Exec(ctx, "CREATE SCHEMA writer_ddl"))
	exec(admin, "CREATE SCHEMA unrelated")
	exec(admin, "CREATE TABLE unrelated.sentinel (id int)")
	denied(manager.Exec(ctx, "DROP TABLE unrelated.sentinel"))
	denied(writer.Exec(ctx, "SELECT id FROM unrelated.sentinel"))

	root := generatedGoProject(t, ctx)
	data, err := os.ReadFile(filepath.Join(root, "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err = json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	const ns = "ponder_go_live"
	const next = "ponder_go_next"
	const views = "ponder_go_stable"
	prepare := func(name string) {
		t.Helper()
		owner := connect(managerURL)
		s, err := store.New(ctx, owner, model, name, schema.Multichain)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.EnableQueryNotifications(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = s.EnableProtocolCache(ctx); err != nil {
			t.Fatal(err)
		}
		if err = owner.Close(); err != nil {
			t.Fatal(err)
		}
	}
	prepare(ns)
	prepare(next)
	grants, err := os.ReadFile("../../docs/examples/postgres-writer.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(manager, string(grants))
	denied(writer.Exec(ctx, "INSERT INTO ponder_go_live._ponder_go_meta VALUES ('wrong','value')"))
	for _, sql := range []string{
		"UPDATE ponder_go_live._ponder_go_meta SET value='changed' WHERE key='fingerprint' RETURNING key",
		"DELETE FROM ponder_go_live._ponder_go_meta WHERE key='deployment' RETURNING key",
	} {
		rows, err := writer.Query(ctx, sql)
		if err != nil || len(rows.Rows) != 0 {
			t.Fatal("protected metadata", rows, err)
		}
	}
	denied(writer.Exec(ctx, "TRUNCATE ponder_go_live.account"))
	denied(writer.Exec(ctx, "DROP TABLE ponder_go_live.account"))
	denied(writer.Exec(ctx, "SELECT * FROM ponder_go_next.account"))

	cli := func(roleURL, op string, args ...string) (string, error) {
		var out bytes.Buffer
		args = append([]string{"db", op, "--database-url", roleURL}, args...)
		err := run(ctx, args, &out)
		return out.String(), err
	}
	prune := func(name, status string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			out, err := cli(managerURL, "prune", "--schema", name)
			if err == nil && strings.Contains(out, `"status":"`+status+`"`) {
				return
			}
			// The writer can hold the transaction lock while prune checks the live lease.
			if err != nil || status != "active" || !strings.Contains(out, `"status":"busy"`) || time.Now().After(deadline) {
				t.Fatalf("prune %s: %s, %v", name, out, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	publish := func(roleURL, source string) error {
		_, err := cli(roleURL, "create-views", "--schema", source, "--views-schema", views)
		return err
	}
	if out, err := cli(managerURL, "list"); err != nil || !strings.Contains(out, ns) || !strings.Contains(out, next) {
		t.Fatal(out, err)
	}
	denied(publish(writerURL, ns))
	if err := publish(managerURL, ns); err != nil {
		t.Fatal(err)
	}
	exec(admin, "REVOKE CREATE ON DATABASE postgres FROM deploy_manager")

	endpoint := testanvil.Start(t, "--no-mining")
	client := rpc.New(endpoint)
	const token = "0x00000000000000000000000000000000000000ab"
	rpcCall := func(method string, args ...any) any {
		t.Helper()
		if args == nil {
			args = []any{}
		}
		var out any
		if err := client.Call(ctx, method, args, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	const topic = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	from := rpcCall("eth_accounts").([]any)[0]
	mine := func(amount int) {
		t.Helper()
		rpcCall("anvil_setCode", token, fmt.Sprintf("0x60%02x600052600260017f%s60206000a300", amount, topic))
		rpcCall("eth_sendTransaction", map[string]any{"from": from, "to": token, "gas": "0x10000"})
		rpcCall("evm_mine")
	}
	snap := rpcCall("evm_snapshot")
	mine(7)
	t.Setenv("PONDER_LOCAL_DATABASE_URL", writerURL)
	t.Setenv("PONDER_LOCAL_RPC_URL", endpoint)
	t.Setenv("PONDER_LOCAL_CONTRACT", token)
	t.Setenv("PONDER_DATABASE_MODE", "existing")
	start := func(want string) {
		t.Helper()
		child, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		var logs bytes.Buffer
		var childErr error
		go func() {
			defer close(done)
			childErr = runCLI(child, []string{"start", "--root", root, "--schema", ns, "--port", "0"}, io.Discard, &logs)
		}()
		defer func() { stop(); <-done }()
		for {
			rows, err := manager.Query(ctx, "SELECT balance::text AS b FROM ponder_go_stable.account ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows.Rows) == 2 && rows.Rows[1]["b"] == want && rows.Rows[0]["b"] == "-"+want {
				break
			}
			select {
			case <-done:
				t.Fatalf("writer exited: %v: %s", childErr, logs.String())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		prune(ns, "active")
	}
	start("7")
	prune(ns, "published")
	if rpcCall("evm_revert", snap) != true {
		t.Fatal("reverting Anvil snapshot")
	}
	mine(9)
	start("9")
	// Rewinding one chain after a peer wrote must create and acknowledge a durable replay generation.
	limited := connect(writerURL)
	db, err := store.OpenExisting(ctx, limited, model, ns, schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	ts := uint64(time.Now().Add(time.Hour).Unix())
	for _, chain := range []uint64{1, 2} {
		event := store.Event{ID: fmt.Sprintf("role-%d", chain), Checkpoint: checkpoint.Block(ts, chain, 2, true)}
		if err := db.Apply(ctx, event, func(w *store.Writer) error {
			return w.Write(ctx, "account", store.Row{"id": fmt.Sprintf("0x0%d", chain+2), "balance": "12"}, store.Insert)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RevertAndRewind(ctx, checkpoint.Block(ts-1, 1, 1, true)); err != nil {
		t.Fatal(err)
	}
	marker, err := db.PendingMultichainReplay(ctx)
	if err != nil || marker == "" {
		t.Fatal("missing replay marker", marker, err)
	}
	if err = db.FinishMultichainReplay(ctx, marker); err != nil {
		t.Fatal(err)
	}
	marker, err = db.PendingMultichainReplay(ctx)
	if err != nil || marker != "" {
		t.Fatal("replay acknowledgement", marker, err)
	}
	if err = limited.Close(); err != nil {
		t.Fatal(err)
	}
	if err := publish(managerURL, next); err != nil {
		t.Fatal(err)
	}
	denied(manager.Exec(ctx, "CREATE SCHEMA manager_after_provisioning"))
	rows, err := manager.Query(ctx, "SELECT id FROM ponder_go_stable.account")
	if err != nil || len(rows.Rows) != 0 {
		t.Fatal("view switch", rows, err)
	}
	_, err = cli(writerURL, "prune", "--schema", ns)
	denied(err)
	prune(ns, "pruned")
	prune(next, "published")
}
