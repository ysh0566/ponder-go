package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestOfflinePGliteCommands(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "data")
	command := func(dir, op string, args ...string) (string, error) {
		var out bytes.Buffer
		err := runCLI(ctx, append([]string{"db", op, "--pglite-dir", dir}, args...), &out, io.Discard)
		return out.String(), err
	}
	if _, err := command(dir, "list"); err == nil {
		t.Fatal("opened a missing cluster")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("administration created the directory", err)
	}
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "PG_VERSION"), []byte("16"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := command(foreign, "prune"); err == nil {
		t.Fatal("opened an unregistered directory")
	}
	if _, err := command(dir, "list", "--database-url", "unused"); err == nil {
		t.Fatal("accepted two database targets")
	}
	open := func() store.Backend {
		t.Helper()
		b, err := store.OpenPGlite(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close() })
		return b
	}
	const old, next, views = "ponder_go_first", "ponder_go_next", "published"
	b := open()
	write := func(name, value, kind string) {
		t.Helper()
		model := schema.Schema{Tables: []schema.Table{{Name: "balance", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "value", Type: kind}}}}}
		s, err := store.New(ctx, b, model, name, schema.Multichain)
		if err != nil {
			t.Fatal(err)
		}
		ev := store.Event{ID: "e", Checkpoint: checkpoint.Block(1, 1, 1, true)}
		if err := s.Apply(ctx, ev, func(w *store.Writer) error {
			return w.Write(ctx, "balance", store.Row{"id": "x", "value": value}, store.Insert)
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(old, "7", "integer")
	write(next, "updated", "text")
	if result, err := store.PruneDeployments(ctx, b, old); err != nil || len(result) != 1 || result[0].Status != "active" {
		t.Fatal("live deployment", result, err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"list", "prune"} {
		if _, err := command(alias, op); err == nil || !strings.Contains(err.Error(), "active") {
			t.Fatalf("opened busy alias for %s: %v", op, err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	must := func(op string, args ...string) string {
		t.Helper()
		out, err := command(alias, op, args...)
		if err != nil {
			t.Fatal(op, args, err)
		}
		return out
	}
	list := must("list")
	if !strings.Contains(list, old) || !strings.Contains(list, next) {
		t.Fatal(list)
	}
	prune := func(ns, status string) {
		t.Helper()
		var got []store.PrunedDeployment
		if err := json.Unmarshal([]byte(must("prune", "--schema", ns)), &got); err != nil || len(got) != 1 || got[0].Status != status || got[0].Schema != ns {
			t.Fatal(got, err)
		}
	}
	must("create-views", "--schema", old, "--views-schema", views)
	prune(old, "published")
	// Changing the column type needs a savepoint and SQLSTATE 42P16 fallback.
	must("create-views", "--schema", next, "--views-schema", views)
	prune(old, "pruned")
	prune(old, "absent")
	prune(next, "published")
	b = open()
	rs, err := b.Query(ctx, "SELECT value FROM published.balance WHERE id='x'")
	if err != nil || len(rs.Rows) != 1 || rs.Rows[0]["value"] != "updated" {
		t.Fatal(rs, err)
	}
}

func TestGeneratedLocalPGlite(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	root := generatedGoProject(t, ctx)
	// Install the already provisioned driver into the generated project; the
	// child must resolve it there even with the upstream override disabled.
	upstream := os.Getenv("PONDER_UPSTREAM")
	if upstream == "" {
		upstream = filepath.Join("..", "..", "..", "ponder-upstream")
	}
	resolve := exec.CommandContext(ctx, "node", "-p", "require.resolve('@electric-sql/pglite',{paths:[process.cwd(),process.argv[1]]})", filepath.Join(upstream, "packages", "core"))
	entry, err := resolve.Output()
	if err != nil {
		t.Fatal("locate the test PGlite module", err)
	}
	scope := filepath.Join(root, "node_modules", "@electric-sql")
	if err := os.MkdirAll(scope, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(filepath.Dir(strings.TrimSpace(string(entry)))), filepath.Join(scope, "pglite")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PONDER_UPSTREAM", "")
	for _, key := range []string{"PONDER_LOCAL_DATABASE_URL", "DATABASE_PRIVATE_URL", "DATABASE_URL", "PONDER_PGLITE_DIR"} {
		t.Setenv(key, "")
	}
	node := testanvil.Start(t)
	client := rpc.New(node)
	const emitter = "0x1000000000000000000000000000000000000001"
	t.Setenv("PONDER_LOCAL_RPC_URL", node)
	t.Setenv("PONDER_LOCAL_CONTRACT", emitter)
	var accounts []string
	if err := client.Call(ctx, "eth_accounts", []any{}, &accounts); err != nil || len(accounts) == 0 {
		t.Fatal(accounts, err)
	}
	// Emit Transfer(0, 1, 25) on the local EVM.
	code := "0x6019600052600160007fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef60206000a300"
	var result any
	if err := client.Call(ctx, "anvil_setCode", []any{emitter, code}, &result); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "eth_sendTransaction", []any{map[string]any{"from": accounts[0], "to": emitter, "gas": "0x186a0"}}, &result); err != nil {
		t.Fatal(err)
	}
	ns, views := "ponder_go_local_e2e", "local_views"
	dir := filepath.Join(root, ".ponder", "pglite")
	for _, mode := range []string{"dev", "start"} {
		if mode == "start" {
			var out bytes.Buffer
			if err := runCLI(ctx, []string{"db", "list", "--pglite-dir", dir}, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			var list store.Result
			if err := json.Unmarshal(out.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			var matches []string
			for _, row := range list.Rows {
				name := fmt.Sprint(row["schema"])
				if strings.HasPrefix(name, ns+"_") && !strings.HasSuffix(name, "_sync") {
					matches = append(matches, name)
				}
			}
			if len(matches) != 1 {
				t.Fatal("expected one dev deployment", list.Rows)
			}
			ns = matches[0]
			moved := filepath.Join(t.TempDir(), "local data")
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			dir = moved
			t.Setenv("PONDER_PGLITE_DIR", dir)
			t.Setenv("DATABASE_URL", "postgres://127.0.0.1:1/unused")
		}
		func() {
			l, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := l.Addr().(*net.TCPAddr).Port
			l.Close()
			child, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			var childErr error
			var diagnostics bytes.Buffer
			go func() {
				defer close(done)
				childErr = runCLI(child, []string{mode, "--root", root, "--hostname", "127.0.0.1", "--port", fmt.Sprint(port), "--schema", ns, "--views-schema", views}, io.Discard, &diagnostics)
			}()
			defer func() {
				stop()
				<-done
				if childErr != nil {
					t.Error(childErr, diagnostics.String())
				}
			}()
			base := fmt.Sprintf("http://127.0.0.1:%d", port)
			for {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ready", nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						break
					}
				}
				select {
				case <-done:
					t.Fatal("indexer stopped", childErr, diagnostics.String())
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(50 * time.Millisecond):
				}
			}
			q := query.Client{BaseURL: base + "/sql"}
			data, err := q.Query(ctx, "SELECT balance FROM account ORDER BY id")
			if err != nil || len(data.Rows) != 2 || fmt.Sprint(data.Rows[0]["balance"]) != "-25" || fmt.Sprint(data.Rows[1]["balance"]) != "25" {
				t.Fatal(data, err)
			}
			if err := runCLI(ctx, []string{"db", "prune", "--pglite-dir", dir}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "active") {
				t.Fatal("active writer was not fenced", err)
			}
		}()
	}
	b, err := store.OpenExistingPGliteDirectory(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rows, err := b.Query(ctx, "SELECT balance FROM local_views.account ORDER BY id")
	if err != nil || len(rows.Rows) != 2 || fmt.Sprint(rows.Rows[1]["balance"]) != "25" {
		t.Fatal(rows, err)
	}
	t.Setenv("PONDER_PGLITE_DIR", filepath.Join(root, "should-not-exist"))
	if err := runCLI(ctx, []string{"serve", "--root", root, "--schema", ns}, io.Discard, io.Discard); err == nil {
		t.Fatal("standalone serve selected PGlite")
	}
	if _, err := os.Stat(os.Getenv("PONDER_PGLITE_DIR")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
