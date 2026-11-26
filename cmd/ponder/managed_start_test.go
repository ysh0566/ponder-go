package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Hold the first full block at the configured start until HTTP readiness has
// been observed. All headers and transaction data come from the local node.
func gateManagedHistory(t *testing.T, endpoint string) (string, <-chan struct{}, func()) {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	next := make(chan struct{})
	entered := make(chan struct{})
	var enteredOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var call struct {
			Method string
			Params []any
		}
		if json.Unmarshal(body, &call) == nil && call.Method == "eth_getBlockByNumber" && len(call.Params) == 2 && call.Params[0] == "0x1" && call.Params[1] == true {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-next:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	release := func() {
		select {
		case <-next:
		default:
			close(next)
		}
	}
	t.Cleanup(release)
	return server.URL, entered, release
}

func setProjectBuildFlags(t *testing.T) {
	t.Helper()
	flags := os.Getenv("GOFLAGS")
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				flags += " -race"
			}
		}
	}
	t.Setenv("GOFLAGS", strings.TrimSpace(flags))
}

func writeManagedFile(t *testing.T, project, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func managedExampleProject(t *testing.T, repo string) string {
	t.Helper()
	project := t.TempDir()
	writeManagedFile(t, project, "go.mod", "module managed.example\n\ngo 1.25.0\n")
	writeManagedFile(t, project, "go.work", fmt.Sprintf("go 1.25.0\nuse (\n.\n%q\n)\n", repo))
	t.Setenv("GOWORK", filepath.Join(project, "go.work"))
	for _, name := range []string{"main.go", "ponder.json"} {
		raw, err := os.ReadFile(filepath.Join(repo, "examples", "managed-multichain", name))
		if err != nil {
			t.Fatal(err)
		}
		writeManagedFile(t, project, name, string(raw))
	}
	return project
}

func TestManagedMultichainCLIStart(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cli := filepath.Join(root, "ponder")
	setProjectBuildFlags(t)
	build := exec.CommandContext(t.Context(), "go", "build", "-o", cli, "./cmd/ponder")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	for _, backend := range []string{"postgres", "pglite", "postgres-serve"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			project := managedExampleProject(t, repo)
			namespace := fmt.Sprintf("ponder_go_cli_%x", time.Now().UnixNano())
			t.Setenv("DATABASE_PRIVATE_URL", "")
			t.Setenv("DATABASE_URL", "")
			t.Setenv("DATABASE_VIEWS_SCHEMA", "")
			t.Setenv("PONDER_PGLITE_DIR", filepath.Join(project, "data"))
			var readerURL string
			var admin store.Backend
			if backend != "pglite" {
				addr := os.Getenv("PONDER_TEST_POSTGRES")
				if backend == "postgres-serve" {
					addr = testpostgres.Start(t).URL()
				}
				if addr == "" {
					t.Skip("requires a task PostgreSQL database")
				}
				t.Setenv("DATABASE_URL", addr)
				db, err := store.OpenPostgres(ctx, addr)
				if err != nil {
					t.Fatal(err)
				}
				if backend == "postgres-serve" {
					admin = db
					// Only this disposable cluster gets a new login.
					password := rand.Text()
					if err := db.Exec(ctx, "CREATE ROLE query_reader LOGIN PASSWORD '"+password+"'"); err != nil {
						t.Fatal(err)
					}
					u, err := url.Parse(addr)
					if err != nil {
						t.Fatal(err)
					}
					u.User = url.UserPassword("query_reader", password)
					params := u.Query()
					params.Set("default_transaction_read_only", "on")
					u.RawQuery = params.Encode()
					readerURL = u.String()
				}
				families := []string{namespace, namespace + "_sync", namespace + "_cache", namespace + "_cache_sync", namespace + "_cache_rpc_1"}
				t.Cleanup(func() { _ = db.Close() })
				for _, name := range families {
					rows, err := db.Query(ctx, "SELECT 1 FROM pg_namespace WHERE nspname=$1", name)
					if err != nil || len(rows.Rows) != 0 {
						t.Fatalf("test namespace %s must be fresh: %v", name, err)
					}
				}
				t.Cleanup(func() {
					for _, ns := range families {
						if err := db.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+" CASCADE"); err != nil {
							t.Error(err)
						}
					}
				})
			}
			one := testanvil.StartChain(t, 1, "--no-mining", "--timestamp", "1700000000")
			two := testanvil.StartChain(t, 2, "--no-mining", "--timestamp", "1700000000")
			mine := func(endpoint string, n int) {
				t.Helper()
				cl := rpc.New(endpoint)
				for i := 0; i < n; i++ {
					var result any
					// Keep the previous block outside the one-second reorg window.
					if err := cl.Call(ctx, "evm_increaseTime", []any{12}, &result); err != nil {
						t.Fatal(err)
					}
					if err := cl.Call(ctx, "evm_mine", []any{}, &result); err != nil {
						t.Fatal(err)
					}
				}
			}
			mine(one, 3)
			mine(two, 3)
			gated, entered, release := gateManagedHistory(t, one)
			cfg, err := os.ReadFile(filepath.Join(project, "ponder.json"))
			if err != nil {
				t.Fatal(err)
			}
			if backend == "postgres-serve" {
				// Make the block mined while the writer is offline historical
				// on reopen, before live-query triggers are reinstalled.
				cfg = bytes.ReplaceAll(cfg, []byte(`"reorgWindow": 1`), []byte(`"reorgWindow": 0`))
			}
			writeManagedFile(t, project, "ponder.json", strings.NewReplacer("http://127.0.0.1:8545", gated, "http://127.0.0.1:8546", two).Replace(string(cfg)))
			launch := func(mode, owner string) (string, func()) {
				t.Helper()
				log, err := os.Create(filepath.Join(project, owner+".log"))
				if err != nil {
					t.Fatal(err)
				}
				args := []string{mode, "--root", project, "--schema", namespace, "--hostname", "127.0.0.1", "--port", "0"}
				if mode == "start" {
					args = append(args, "--shutdown-timeout", "30s")
				}
				cmd := exec.CommandContext(ctx, cli, args...)
				if mode == "serve" {
					cmd.Env = append(os.Environ(), "DATABASE_PRIVATE_URL="+readerURL, "DATABASE_URL=")
				}
				cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
				cmd.WaitDelay = 40 * time.Second
				cmd.Stdout, cmd.Stderr = log, log
				if err := cmd.Start(); err != nil {
					log.Close()
					t.Fatal(err)
				}
				log.Close()
				done := make(chan struct{})
				var exit error
				go func() { exit = cmd.Wait(); close(done) }()
				output := func() string { body, _ := os.ReadFile(log.Name()); return string(body) }
				stopped := false
				stop := func() {
					t.Helper()
					if stopped {
						return
					}
					stopped = true
					_ = cmd.Process.Signal(syscall.SIGTERM)
					select {
					case <-done:
						if exit != nil {
							t.Errorf("%s exit: %v\n%s", owner, exit, output())
						}
					case <-time.After(40 * time.Second):
						_ = cmd.Process.Kill()
						<-done
						t.Errorf("%s did not stop: %s", owner, output())
					}
				}
				t.Cleanup(stop)
				for {
					for _, line := range strings.Split(output(), "\n") {
						if addr, ok := strings.CutPrefix(line, "Listening on "); ok {
							if !strings.HasPrefix(addr, "http://127.0.0.1:") {
								t.Fatalf("unexpected listener %q", addr)
							}
							return addr, stop
						}
					}
					select {
					case <-done:
						t.Fatalf("%s exited: %v\n%s", owner, exit, output())
					case <-ctx.Done():
						t.Fatalf("listener: %v\n%s", ctx.Err(), output())
					case <-time.After(25 * time.Millisecond):
					}
				}
			}
			get := func(path string) (int, []byte) {
				t.Helper()
				req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
				if err != nil {
					t.Fatal(err)
				}
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				return res.StatusCode, body
			}
			const sql = `{"json":{"sql":"select chain, count(*)::text as n from indexed_block group by chain order by chain","params":[]}}`
			arg := "?sql=" + url.QueryEscape(sql)
			checkRows := func(addr string, n1, n2 int) {
				t.Helper()
				want := fmt.Sprintf("one:%d two:%d", n1, n2)
				var got string
				for {
					code, data := get(addr + "/sql/db" + arg)
					var result store.Result
					if code != 200 || json.Unmarshal(data, &result) != nil {
						t.Fatalf("SQL: %d %s", code, data)
					}
					if len(result.Rows) == 2 {
						got = fmt.Sprintf("%s:%v %s:%v", result.Rows[0]["chain"], result.Rows[0]["n"], result.Rows[1]["chain"], result.Rows[1]["n"])
						if got == want {
							break
						}
					}
					select {
					case <-ctx.Done():
						t.Fatalf("rows %v, want %s", got, want)
					case <-time.After(50 * time.Millisecond):
					}
				}
				code, data := get(addr + "/status")
				var status map[string]query.ChainStatus
				if code != 200 || json.Unmarshal(data, &status) != nil || status["one"].ID != 1 || status["two"].ID != 2 || status["one"].Block.Number != uint64(n1) || status["two"].Block.Number != uint64(n2) {
					t.Fatalf("status: %d %s", code, data)
				}
			}
			ready := func(addr string) {
				t.Helper()
				for {
					code, body := get(addr + "/ready")
					if code == 200 {
						return
					}
					if code != 503 {
						t.Fatalf("readiness: %d %s", code, body)
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(50 * time.Millisecond):
					}
				}
			}
			addr, stop := launch("start", "first")
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("writer did not request initial block", ctx.Err())
			}
			if code, _ := get(addr + "/health"); code != 200 {
				t.Fatalf("health during backfill %d", code)
			}
			if code, _ := get(addr + "/ready"); code != 503 {
				t.Fatalf("premature readiness %d", code)
			}
			if code, _ := get(addr + "/sql/live" + arg); code != 503 {
				t.Fatalf("premature live query %d", code)
			}
			var readerAddr string
			var stopReader func()
			var activation store.NotificationListener
			if backend == "postgres-serve" {
				relations := []string{"indexed_block", "_ponder_go_meta", "_ponder_go_app", "_ponder_go_chains", "_ponder_go_progress"}
				for i, name := range relations {
					relations[i] = schema.Quote(namespace) + "." + schema.Quote(name)
				}
				if err := admin.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema.Quote(namespace)+" TO query_reader"); err != nil {
					t.Fatal(err)
				}
				if err := admin.Exec(ctx, "GRANT SELECT ON "+strings.Join(relations, ",")+" TO query_reader"); err != nil {
					t.Fatal(err)
				}
				// Opening a mismatched or missing namespace must leave the database intact.
				readonly, err := store.OpenPostgres(ctx, readerURL)
				if err != nil {
					t.Fatal(err)
				}
				defer readonly.Close()
				activation, err = store.Listen(ctx, readonly, namespace+"_live_query")
				if err != nil {
					t.Fatal(err)
				}
				defer activation.Close()
				if r, err := store.OpenManagedQueryReader(ctx, readonly, schema.Schema{}, namespace, schema.Multichain); err == nil || r != nil {
					t.Fatal("opened a mismatched business model")
				}
				if r, err := store.OpenManagedQueryReader(ctx, readonly, schema.Schema{}, namespace+"_absent", schema.Multichain); err == nil || r != nil {
					t.Fatal("opened a missing namespace")
				}
				check, err := admin.Query(ctx, "SELECT has_schema_privilege('query_reader',$1,'USAGE') AS cache_access, has_table_privilege('query_reader',$2,'INSERT') AS can_write, to_regnamespace($3)::text AS absent", namespace+"_cache", schema.Quote(namespace)+".indexed_block", namespace+"_absent")
				if err != nil || len(check.Rows) != 1 || check.Rows[0]["cache_access"] != false || check.Rows[0]["can_write"] != false || check.Rows[0]["absent"] != nil {
					t.Fatalf("reader privileges or namespace changed: %+v %v", check, err)
				}
				// The standalone reader must start without the chain configuration.
				path := filepath.Join(project, "ponder.json")
				if err := os.Rename(path, path+".writer"); err != nil {
					t.Fatal(err)
				}
				readerAddr, stopReader = launch("serve", "reader")
				if err := os.Rename(path+".writer", path); err != nil {
					t.Fatal(err)
				}
				if code, _ := get(readerAddr + "/ready"); code != 503 {
					t.Fatalf("standalone readiness before catchup %d", code)
				}
				if code, _ := get(readerAddr + "/sql/live" + arg); code != 503 {
					t.Fatalf("standalone live query during backfill %d", code)
				}
			}
			release()
			ready(addr)
			code, body := get(addr + "/metrics")
			if code != http.StatusOK {
				t.Fatalf("metrics: %d", code)
			}
			for _, name := range []string{"one", "two"} {
				sample := fmt.Sprintf(`ponder_rpc_request_duration_count{chain="%s",method="eth_getBlockByNumber"}`, name)
				if !bytes.Contains(body, []byte(sample)) {
					t.Fatalf("missing writer RPC sample %s", sample)
				}
			}
			checkRows(addr, 3, 3)
			streamAddr := addr
			if readerAddr != "" {
				ready(readerAddr)
				checkRows(readerAddr, 3, 3)
				// No further block has been mined. Activation must notify readers
				// about the rows committed before the live triggers existed.
				wait, cancelWait := context.WithTimeout(ctx, 10*time.Second)
				defer cancelWait()
				for {
					event, err := activation.Wait(wait)
					if err != nil {
						t.Fatalf("historical activation notification: %v", err)
					}
					if event.Payload == `["indexed_block"]` {
						break
					}
				}
				streamAddr = readerAddr
			}
			streamCtx, closeStream := context.WithCancel(ctx)
			defer closeStream()
			req, _ := http.NewRequestWithContext(streamCtx, "GET", streamAddr+"/sql/live"+arg, nil)
			stream, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Body.Close()
			if stream.StatusCode != 200 {
				t.Fatalf("stream status %d", stream.StatusCode)
			}
			mine(one, 1)
			scan := bufio.NewScanner(stream.Body)
			expectStream := func(one, two string) {
				t.Helper()
				for scan.Scan() {
					if payload, ok := strings.CutPrefix(scan.Text(), "data: "); ok {
						var result store.Result
						if err := json.Unmarshal([]byte(payload), &result); err != nil || len(result.Rows) != 2 || result.Rows[0]["chain"] != "one" || result.Rows[0]["n"] != one || result.Rows[1]["n"] != two {
							t.Fatalf("stream payload %s", payload)
						}
						return
					}
				}
				t.Fatalf("no notification for %s/%s: %v", one, two, scan.Err())
			}
			expectStream("4", "3")
			if readerAddr == "" {
				closeStream()
			}
			checkRows(addr, 4, 3)
			stop()
			if readerAddr != "" {
				checkRows(readerAddr, 4, 3)
			}
			mine(two, 1)
			addr, stop = launch("start", "recovered")
			ready(addr)
			checkRows(addr, 4, 4)
			if readerAddr != "" {
				// The writer catches up before reinstalling its notification triggers.
				// This existing subscription must refresh without another live block.
				expectStream("4", "4")
				checkRows(readerAddr, 4, 4)
				closeStream()
				stopReader()
			}
			mine(two, 1)
			checkRows(addr, 4, 5)
			stop()
		})
	}
}
