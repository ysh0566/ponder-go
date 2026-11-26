package main

import (
	"bufio"
	"bytes"
	"context"
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
	"syscall"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

// Hold the first full block at the configured start until HTTP readiness has
// been observed. All headers and transaction data come from the local node.
func gateManagedHistory(t *testing.T, endpoint string) (string, func()) {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	next := make(chan struct{})
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
	return server.URL, release
}

func TestManagedMultichainCLIStart(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cli := filepath.Join(root, "ponder")
	flags := os.Getenv("GOFLAGS")
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-race" && setting.Value == "true" {
				flags += " -race"
			}
		}
	}
	t.Setenv("GOFLAGS", strings.TrimSpace(flags))
	build := exec.CommandContext(t.Context(), "go", "build", "-o", cli, "./cmd/ponder")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			project := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module managed.example\n\ngo 1.25.0\n")
			write("go.work", fmt.Sprintf("go 1.25.0\nuse (\n.\n%q\n)\n", repo))
			t.Setenv("GOWORK", filepath.Join(project, "go.work"))
			for _, name := range []string{"main.go", "ponder.json"} {
				raw, err := os.ReadFile(filepath.Join(repo, "examples", "managed-multichain", name))
				if err != nil {
					t.Fatal(err)
				}
				write(name, string(raw))
			}
			namespace := fmt.Sprintf("ponder_go_cli_%x", time.Now().UnixNano())
			t.Setenv("DATABASE_PRIVATE_URL", "")
			t.Setenv("DATABASE_URL", "")
			t.Setenv("DATABASE_VIEWS_SCHEMA", "")
			t.Setenv("PONDER_PGLITE_DIR", filepath.Join(project, "data"))
			if backend == "postgres" {
				addr := os.Getenv("PONDER_TEST_POSTGRES")
				if addr == "" {
					t.Skip("requires a task PostgreSQL database")
				}
				t.Setenv("DATABASE_URL", addr)
				db, err := store.OpenPostgres(ctx, addr)
				if err != nil {
					t.Fatal(err)
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
			one := testanvil.StartChain(t, 1, "--no-mining")
			two := testanvil.StartChain(t, 2, "--no-mining")
			mine := func(endpoint string, n int) {
				t.Helper()
				cl := rpc.New(endpoint)
				for i := 0; i < n; i++ {
					var result any
					if err := cl.Call(ctx, "evm_mine", []any{}, &result); err != nil {
						t.Fatal(err)
					}
				}
			}
			mine(one, 3)
			mine(two, 3)
			gated, release := gateManagedHistory(t, one)
			cfg, err := os.ReadFile(filepath.Join(project, "ponder.json"))
			if err != nil {
				t.Fatal(err)
			}
			write("ponder.json", strings.NewReplacer("http://127.0.0.1:8545", gated, "http://127.0.0.1:8546", two).Replace(string(cfg)))
			start := func(owner string) (string, func()) {
				t.Helper()
				log, err := os.Create(filepath.Join(project, owner+".log"))
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"start", "--root", project, "--schema", namespace, "--hostname", "127.0.0.1", "--port", "0"}
				if backend == "pglite" {
					args = append(args, "--shutdown-timeout", "30s")
				}
				cmd := exec.CommandContext(ctx, cli, args...)
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
			addr, stop := start("first")
			if code, _ := get(addr + "/health"); code != 200 {
				t.Fatalf("health during backfill %d", code)
			}
			if code, _ := get(addr + "/ready"); code != 503 {
				t.Fatalf("premature readiness %d", code)
			}
			if code, _ := get(addr + "/sql/live" + arg); code != 503 {
				t.Fatalf("premature live query %d", code)
			}
			release()
			ready(addr)
			checkRows(addr, 3, 3)
			streamCtx, closeStream := context.WithTimeout(ctx, time.Minute)
			defer closeStream()
			req, _ := http.NewRequestWithContext(streamCtx, "GET", addr+"/sql/live"+arg, nil)
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
			delivered := false
			for scan.Scan() {
				if payload, ok := strings.CutPrefix(scan.Text(), "data: "); ok {
					var result store.Result
					if err := json.Unmarshal([]byte(payload), &result); err != nil || len(result.Rows) != 2 || result.Rows[0]["chain"] != "one" || result.Rows[0]["n"] != "4" || result.Rows[1]["n"] != "3" {
						t.Fatalf("stream payload %s", payload)
					}
					delivered = true
					break
				}
			}
			closeStream()
			if !delivered {
				t.Fatalf("no notification for live block: %v", scan.Err())
			}
			checkRows(addr, 4, 3)
			stop()
			mine(two, 1)
			addr, stop = start("recovered")
			ready(addr)
			checkRows(addr, 4, 4)
			mine(two, 1)
			checkRows(addr, 4, 5)
			stop()
		})
	}
}
