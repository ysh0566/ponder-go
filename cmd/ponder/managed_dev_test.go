package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/internal/testpostgres"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func TestManagedMultichainCLIDev(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	setManagedBuildFlags(t)
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 7*time.Minute)
			defer cancel()
			root := managedExampleProject(t, repo)
			read := func(name string) string {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			t.Setenv("DATABASE_PRIVATE_URL", "")
			t.Setenv("DATABASE_URL", "")
			t.Setenv("DATABASE_VIEWS_SCHEMA", "production_views")
			t.Setenv("PONDER_PGLITE_DIR", filepath.Join(root, ".ponder"))
			var pg store.Backend
			if backend == "postgres" {
				database := testpostgres.Start(t).URL()
				t.Setenv("DATABASE_URL", database)
				pg, err = store.OpenPostgres(ctx, database)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
			}
			one := testanvil.StartChain(t, 1, "--no-mining")
			two := testanvil.StartChain(t, 2, "--no-mining")
			mine := func(endpoint string) {
				t.Helper()
				var result any
				if err := rpc.New(endpoint).Call(ctx, "evm_mine", []any{}, &result); err != nil {
					t.Fatal(err)
				}
			}
			mine(one)
			mine(two)
			writeManagedFile(t, root, "ponder.json", strings.NewReplacer("http://127.0.0.1:8545", one, "http://127.0.0.1:8546", two).Replace(read("ponder.json")))
			const prefix = "ponder_go_managed_development"
			project := nativeProject{root: root, pkg: ".", namespace: prefix}
			log, err := os.Create(filepath.Join(t.TempDir(), "dev.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			done := make(chan error, 1)
			go func() {
				done <- runCLI(ctx, []string{"dev", "--disable-ui", "--root", root, "--schema", prefix, "--port", "0", "--hostname", "127.0.0.1", "--shutdown-timeout", "30s"}, log, log)
			}()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			output := func() string { b, _ := os.ReadFile(log.Name()); return string(b) }
			wait := func() {
				t.Helper()
				select {
				case <-ctx.Done():
					t.Fatalf("dev timeout: %s", output())
				case <-time.After(100 * time.Millisecond):
				}
			}
			listener := func(n int) string {
				t.Helper()
				for {
					text := output()
					if strings.Contains(text, "Indexer exited, waiting for an edit") {
						t.Fatal(text)
					}
					count := 0
					for line := range strings.SplitSeq(text, "\n") {
						if addr, ok := strings.CutPrefix(line, "Listening on "); ok {
							count++
							if count == n && strings.HasPrefix(addr, "http://127.0.0.1:") {
								return addr
							}
						}
					}
					wait()
				}
			}
			checkRows := func(addr, label string, first, second int) {
				t.Helper()
				const sql = `{"json":{"sql":"select chain,count(*)::text as n from indexed_block group by chain order by chain","params":[]}}`
				for {
					req, err := http.NewRequestWithContext(ctx, "GET", addr+"/sql/db?sql="+url.QueryEscape(sql), nil)
					if err != nil {
						t.Fatal(err)
					}
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatalf("query %s: %v\n%s", addr, err, output())
					}
					var rows store.Result
					err = json.NewDecoder(res.Body).Decode(&rows)
					res.Body.Close()
					if res.StatusCode == 200 && err == nil && len(rows.Rows) == 2 &&
						rows.Rows[0]["chain"] == "one"+label && rows.Rows[0]["n"] == fmt.Sprint(first) &&
						rows.Rows[1]["chain"] == "two"+label && rows.Rows[1]["n"] == fmt.Sprint(second) {
						return
					}
					wait()
				}
			}
			revision := func() string {
				t.Helper()
				r, err := project.projectRevision()
				if err != nil {
					t.Fatal(err)
				}
				return r
			}

			first := listener(1)
			checkRows(first, "", 1, 1)
			initial := revision()
			source := read("main.go")
			writeManagedFile(t, root, "main.go", source+"\nvar _ = brokenDevEdit\n")
			for !strings.Contains(output(), "undefined: brokenDevEdit") || !strings.Contains(output(), "Go build failed, waiting for an edit") {
				wait()
			}
			mine(one)
			checkRows(first, "", 2, 1)

			edited := strings.Replace(source, `"chain": chain,`, `"chain": chain + "-v2",`, 1)
			if edited == source {
				t.Fatal("handler edit did not match")
			}
			writeManagedFile(t, root, "main.go", edited)
			second := listener(2)
			checkRows(second, "-v2", 2, 1)
			mine(two)
			checkRows(second, "-v2", 2, 2)
			afterEdit := revision()
			checkDeployment := func(rev string, locked string) {
				t.Helper()
				name := project.devNamespace(rev)
				if pg != nil {
					rows, err := pg.Query(ctx, "SELECT value->>'build_id' AS build,value->>'is_locked' AS locked,value->>'is_dev' AS dev,to_regnamespace('production_views')::text AS shared_views FROM "+schema.Quote(name)+"._ponder_go_app WHERE key='app'")
					if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["build"] != rev || rows.Rows[0]["locked"] != locked || rows.Rows[0]["dev"] != "1" || rows.Rows[0]["shared_views"] != nil {
						t.Fatalf("deployment %s: %+v %v", name, rows, err)
					}
					return
				}
				for _, domain := range []string{"business", "sync"} {
					if st, err := os.Stat(filepath.Join(root, ".ponder", name, domain)); err != nil || !st.IsDir() {
						t.Fatalf("missing %s directory for %s: %v", domain, name, err)
					}
				}
			}
			if initial == afterEdit || len(project.devNamespace(initial)) != 45 {
				t.Fatal("expected distinct revisions with a full-length shadow namespace")
			}
			checkDeployment(initial, "0")
			checkDeployment(afterEdit, "1")
			writeManagedFile(t, root, "main.go", source)
			checkRows(listener(3), "", 2, 2)
			if revision() != initial {
				t.Fatal("restoring project files did not restore the original revision")
			}
			checkDeployment(initial, "1")
			checkDeployment(afterEdit, "0")
		})
	}
}
