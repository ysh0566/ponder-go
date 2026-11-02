package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
	"time"
)

type setupCrashSpec struct {
	RPC, Marker string
	Storage     store.IndependentStorageOptions
}

func setupCrashHandler(calls *int) indexing.Handler {
	return func(ctx context.Context, a *indexing.HandlerArgs) error {
		*calls++
		_, err := a.Context.DB.InsertOne(ctx, "account", store.Row{"id": fmt.Sprintf("init-%d", finiteChain(a)), "balance": big.NewInt(99)}, store.Conflict{})
		return err
	}
}
func setupCrashChild(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec setupCrashSpec
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	p := &finiteRPC{server: &httptest.Server{URL: spec.RPC}}
	calls := 0
	b := finiteBuild(t, p, 2, true, indexing.Handler(finiteIncrement), setupCrashHandler(&calls))
	epochs := []uint64{}
	var r *RuntimeRunner
	o := RuntimeOptions{Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: spec.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "setup-process-crash-v8", OpenID: "setup-crash-child", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}, OnSetupCommitted: func(ctx context.Context) error {
		c, err := r.Control(ctx)
		if err != nil || c.Batches != 1 || c.Bootstraps != 1 || c.Serving || calls != 2 {
			os.Exit(86)
		}
		if ready, e := r.Ready(ctx); e != nil || ready {
			os.Exit(87)
		}
		proof, _ := json.Marshal(map[string]any{"phase": "setup_committed_before_events", "pid": os.Getpid(), "exit": 83, "batches": c.Batches, "bootstraps": c.Bootstraps, "postgres_engine_stays_running": true})
		if err = os.WriteFile(spec.Marker, proof, 0600); err != nil {
			os.Exit(88)
		}
		os.Exit(83)
		return nil
	}}
	r, err = OpenRuntimePostgres(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background())
	t.Fatalf("child failed to exit without defers: %v", err)
}

// Abrupt application exit; the independent PostgreSQL engine stays running.
func TestRuntimeSetupCrash(t *testing.T) {
	if path := os.Getenv("PONDER_M45_SETUP_CRASH_SPEC"); path != "" {
		setupCrashChild(t, path)
		return
	}
	t.Run("postgres/committed_before_events", func(t *testing.T) {
		continuousRoot(t)
		p := newFiniteRPC(t, 2)
		calls := 0
		b := finiteBuild(t, p, 2, true, indexing.Handler(finiteIncrement), setupCrashHandler(&calls))
		f := newManagedFiniteFixture(t, "postgres", b)
		dir, err := os.MkdirTemp(os.Getenv("PONDER_M45_TEST_ROOT"), "setup-app-crash-")
		if err != nil {
			t.Fatal(err)
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || !strings.HasPrefix(real, "/root/repo/") {
			t.Fatalf("crash proof outside storage %s %v", real, err)
		}
		marker := filepath.Join(dir, "exit-marker.json")
		raw, err := json.Marshal(setupCrashSpec{RPC: p.server.URL, Marker: marker, Storage: f.options.Owned.Storage})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "child-spec.json")
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRuntimeSetupCrash$", "-test.v")
		cmd.Env = append(os.Environ(), "PONDER_M45_SETUP_CRASH_SPEC="+path)
		output, err := cmd.CombinedOutput()
		if e := os.WriteFile(filepath.Join(dir, "child.log"), output, 0600); e != nil {
			t.Fatal(e)
		}
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.ExitCode() != 83 || ctx.Err() != nil {
			t.Fatalf("application exit=%v ctx=%v output=%s", err, ctx.Err(), output)
		}
		proof, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		var marked struct {
			Phase               string
			Exit                int
			Batches, Bootstraps uint64
		}
		if err = json.Unmarshal(proof, &marked); err != nil || marked.Phase != "setup_committed_before_events" || marked.Exit != 83 || marked.Batches != 1 || marked.Bootstraps != 1 {
			t.Fatalf("crash boundary marker=%s err=%v", proof, err)
		}
		managedInspect(t, f, func(db store.Backend) {
			ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
			rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+ns+".account ORDER BY id")
			if len(rows) != 2 || fmt.Sprint(rows[0]["id"]) != "init-1" || fmt.Sprint(rows[1]["id"]) != "init-2" || fmt.Sprint(rows[0]["balance"]) != "99" || fmt.Sprint(rows[1]["balance"]) != "99" {
				t.Fatalf("native crash boundary committed setup/event rows=%v", rows)
			}
		})
		epochs := []uint64{}
		o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "setup-process-crash-v8", OpenID: "setup-crash-parent", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}, OnSetupCommitted: func(context.Context) error { return fmt.Errorf("committed setup observer repeated after crash") }}
		r := runtimeOpen(t, "postgres", o)
		if ready, e := r.Ready(context.Background()); e != nil || ready {
			t.Fatalf("ready before actual replay %v %v", ready, e)
		}
		out, err := r.Run(context.Background())
		if err != nil || !out.Completed || !out.Ready || !out.Recovered || calls != 0 {
			t.Fatalf("crash setup repeated/lost %+v calls=%d err=%v", out, calls, err)
		}
		if err = r.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		managedInspect(t, f, func(db store.Backend) {
			ns := schema.Quote(f.options.Owned.Storage.BusinessNamespace)
			rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+ns+".account ORDER BY id")
			if len(rows) != 4 || fmt.Sprint(rows[0]["balance"]) != "2" || fmt.Sprint(rows[1]["balance"]) != "2" || fmt.Sprint(rows[2]["balance"]) != "99" || fmt.Sprint(rows[3]["balance"]) != "99" {
				t.Fatalf("actual crash recovery rows=%v", rows)
			}
		})
	})
}
