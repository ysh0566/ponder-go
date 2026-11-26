package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/checkpoint"
	"github.com/ysh0566/ponder-go/internal/testanvil"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func generatedGoProject(t *testing.T, ctx context.Context) string {
	t.Helper()
	return generatedGoTemplate(t, ctx, "go")
}

func generatedGoTemplate(t *testing.T, ctx context.Context, template string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "reader")
	if err := run(ctx, []string{"init", "--template", template, root}, io.Discard); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"mod", "edit", "-replace=github.com/ysh0566/ponder-go=" + repo}, {"mod", "tidy"}} {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prepare generated project: %s %v", out, err)
		}
	}
	return root
}

func TestGeneratedGoServe(t *testing.T) {
	address := os.Getenv("PONDER_TEST_POSTGRES")
	if address == "" {
		t.Skip("requires a task PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	b, err := store.OpenPostgres(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	root := generatedGoProject(t, ctx)
	raw, err := os.ReadFile(filepath.Join(root, "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("ponder_go_reader_%x", time.Now().UnixNano())
	defer func() {
		if err := b.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+", "+schema.Quote(ns+"_sync")+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	writer, err := store.New(ctx, b, model, ns, schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.EnableQueryNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	readOnly, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	options := readOnly.Query()
	options.Set("default_transaction_read_only", "on")
	readOnly.RawQuery = options.Encode()
	t.Setenv("PONDER_LOCAL_DATABASE_URL", readOnly.String())
	t.Setenv("PONDER_LOCAL_RPC_URL", "")
	t.Setenv("PONDER_LOCAL_CONTRACT", "")
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	serviceCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	var childErr error
	var diagnostics strings.Builder
	go func() {
		defer close(done)
		childErr = runCLI(serviceCtx, []string{"--log-format=json", "serve", "-v", "--root", root, "--hostname", "127.0.0.1", "--port", fmt.Sprint(port), "--schema", ns}, io.Discard, &diagnostics)
	}()
	defer func() { stop(); <-done }()
	health := &http.Client{Timeout: time.Second}
	for {
		r, err := health.Get(base + "/ready")
		if err == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-done:
			t.Fatalf("generated query server exited: %v", childErr)
		case <-ctx.Done():
			t.Fatal("generated query server did not become ready", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	statement := "SELECT id,balance FROM account"
	params := url.Values{"sql": {`{"json":{"sql":"SELECT id,balance FROM account","params":[]}}`}}
	streamCtx, endStream := context.WithTimeout(ctx, 15*time.Second)
	defer endStream()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, base+"/sql/live?"+params.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	id := "0x0000000000000000000000000000000000000001"
	event := store.Event{ID: "transfer", Checkpoint: checkpoint.Block(100, 1, 1, true)}
	if err := writer.Apply(ctx, event, func(w *store.Writer) error {
		return w.Write(ctx, "account", store.Row{"id": id, "balance": "9"}, store.Insert)
	}); err != nil {
		t.Fatal(err)
	}
	client := query.Client{BaseURL: base + "/sql"}
	check := func(rows []map[string]any, want int) {
		t.Helper()
		if len(rows) != want || (want == 1 && (rows[0]["id"] != id || fmt.Sprint(rows[0]["balance"]) != "9")) {
			t.Fatalf("unexpected query rows %v", rows)
		}
	}
	scanner := bufio.NewScanner(response.Body)
	next := func(want int) {
		t.Helper()
		for scanner.Scan() {
			raw, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var data store.Result
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				t.Fatal(err)
			}
			check(data.Rows, want)
			return
		}
		t.Fatalf("missing SSE result: %v", scanner.Err())
	}
	next(1)
	data, err := client.Query(ctx, statement)
	if err != nil {
		t.Fatal(err)
	}
	check(data.Rows, 1)
	if err := writer.Revert(ctx, checkpoint.Block(99, 1, 0, true)); err != nil {
		t.Fatal(err)
	}
	next(0)
	response.Body.Close()
	stop()
	<-done
	entries := parseLogRecords(t, diagnostics.String())
	var startup, build bool
	for _, r := range entries {
		if r["schema"] == ns && r["address"] != "" && r["level"] == float64(30) {
			startup = true
		}
		if r["msg"] == "Building Go project" && r["level"] == float64(20) {
			build = true
		}
	}
	if !startup || !build {
		t.Fatalf("missing structured startup/build records: %v", entries)
	}
}

func TestGeneratedGoStartViews(t *testing.T) {
	address := os.Getenv("PONDER_TEST_POSTGRES")
	if address == "" {
		t.Skip("requires a task PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	root := generatedGoProject(t, ctx)
	backend, err := store.OpenPostgres(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	source := fmt.Sprintf("ponder_go_start_%x", time.Now().UnixNano())
	views := source + "_v"
	defer func() {
		if err := backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(views)+", "+schema.Quote(source)+", "+schema.Quote(source+"_sync")+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	t.Setenv("PONDER_LOCAL_DATABASE_URL", address)
	t.Setenv("PONDER_LOCAL_RPC_URL", testanvil.Start(t, "--no-mining"))
	t.Setenv("PONDER_LOCAL_CONTRACT", "0x00000000000000000000000000000000000000ab")
	child, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	var childErr error
	go func() {
		defer close(done)
		childErr = run(child, []string{"start", "--root", root, "--schema", source, "--views-schema", views, "--port", "0"}, io.Discard)
	}()
	defer func() { stop(); <-done }()
	for {
		found, err := backend.Query(ctx, "SELECT to_regclass($1)::text AS name", schema.Quote(views)+".account")
		if err != nil {
			t.Fatal(err)
		}
		if found.Rows[0]["name"] != nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("generated start stopped before publishing: %v", childErr)
		case <-ctx.Done():
			t.Fatal("generated start did not publish", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	rows, err := backend.Query(ctx, "SELECT chain_id::text AS id FROM "+schema.Quote(views)+"._ponder_checkpoint")
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["id"] != "1" {
		t.Fatalf("published checkpoint: %v, %v", rows, err)
	}
}
