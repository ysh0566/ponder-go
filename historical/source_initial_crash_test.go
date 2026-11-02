package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func sourceCrashChild(t *testing.T, path string) {
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var spec setupCrashSpec
	if e = json.Unmarshal(raw, &spec); e != nil {
		t.Fatal(e)
	}
	p := &finiteRPC{server: &httptest.Server{URL: spec.RPC}}
	calls := 0
	b := sourceMatrixBuild(t, p, 1, 2, 1, "both", false, indexing.Handler(finiteIncrement), setupCrashHandler(&calls))
	epochs := []uint64{}
	var r *RuntimeRunner
	o := RuntimeOptions{SourceCheckpoints: true, Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: spec.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "source-process-crash-v9", OpenID: "source-crash-child", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnProgress: func(ctx context.Context, p RuntimeLiveProgress) error {
		if p.Type != "source-block" {
			return nil
		}
		c, e := r.Control(ctx)
		if e != nil || c.Batches != 2 || c.Tail != 1 || c.Finalizations != 0 || c.Serving || calls != 2 {
			os.Exit(86)
		}
		heads, e := r.PublicCheckpoints(ctx)
		if e != nil {
			os.Exit(87)
		}
		proof, _ := json.Marshal(map[string]any{"phase": "source_block_committed_before_finalize", "pid": os.Getpid(), "exit": 84, "tail": c.Tail, "finalizations": c.Finalizations, "public": heads, "postgres_engine_stays_running": true})
		if e = os.WriteFile(spec.Marker, proof, 0600); e != nil {
			os.Exit(88)
		}
		os.Exit(84)
		return nil
	}}}
	r, e = OpenRuntimePostgres(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.Run(context.Background())
	t.Fatalf("child did not exit %v", e)
}

// Abrupt application exit with PostgreSQL left running, not an engine crash.
func TestRuntimeSourceCrash(t *testing.T) {
	if path := os.Getenv("PONDER_M45_SOURCE_CRASH_SPEC"); path != "" {
		sourceCrashChild(t, path)
		return
	}
	t.Run("postgres/committed_before_finalize", func(t *testing.T) {
		continuousRoot(t)
		s := newContinuousRPC(t, 2)
		calls := 0
		b := sourceMatrixBuild(t, s.p, 1, 2, 1, "both", false, indexing.Handler(finiteIncrement), setupCrashHandler(&calls))
		f := newManagedFiniteFixture(t, "postgres", b)
		dir, e := os.MkdirTemp(os.Getenv("PONDER_M45_TEST_ROOT"), "source-app-crash-")
		if e != nil {
			t.Fatal(e)
		}
		real, e := filepath.EvalSymlinks(dir)
		if e != nil || !strings.HasPrefix(real, "/root/repo/") {
			t.Fatalf("crash storage path %s %v", real, e)
		}
		marker := filepath.Join(dir, "exit-marker.json")
		spec, e := json.Marshal(setupCrashSpec{RPC: s.p.server.URL, Marker: marker, Storage: f.options.Owned.Storage})
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(dir, "child-spec.json")
		if e = os.WriteFile(path, spec, 0600); e != nil {
			t.Fatal(e)
		}
		exe, e := os.Executable()
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRuntimeSourceCrash$", "-test.v")
		cmd.Env = append(os.Environ(), "PONDER_M45_SOURCE_CRASH_SPEC="+path)
		output, e := cmd.CombinedOutput()
		if x := os.WriteFile(filepath.Join(dir, "child.log"), output, 0600); x != nil {
			t.Fatal(x)
		}
		var exited *exec.ExitError
		if !errors.As(e, &exited) || exited.ExitCode() != 84 || ctx.Err() != nil {
			t.Fatalf("native app exit=%v timeout=%v output=%s", e, ctx.Err(), output)
		}
		raw, e := os.ReadFile(marker)
		if e != nil {
			t.Fatal(e)
		}
		var proof struct {
			Phase               string
			Exit                int
			Tail, Finalizations uint64
			Public              map[uint64]string
		}
		if e = json.Unmarshal(raw, &proof); e != nil || proof.Phase != "source_block_committed_before_finalize" || proof.Exit != 84 || proof.Tail != 1 || proof.Finalizations != 0 {
			t.Fatalf("crash marker %s %v", raw, e)
		}
		managedInspect(t, f, func(db store.Backend) {
			rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
			if len(rows) != 3 || fmt.Sprint(rows[0]["balance"]) != "1" || fmt.Sprint(rows[1]["balance"]) != "99" || fmt.Sprint(rows[2]["balance"]) != "99" {
				t.Fatalf("actual post-crash rows %v", rows)
			}
		})
		epochs := []uint64{}
		o := RuntimeOptions{SourceCheckpoints: true, Historical: runtimeFixtureInput(f), BuildID: "source-process-crash-v9", OpenID: "source-crash-parent", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}, OnSetupCommitted: func(context.Context) error { return fmt.Errorf("setup repeated after committed crash") }}
		r := runtimeOpen(t, "postgres", o)
		heads, e := r.PublicCheckpoints(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		for id, want := range proof.Public {
			if heads[id] != want {
				t.Fatalf("recovery moved public before replay %v -> %v", proof.Public, heads)
			}
		}
		c, e := r.Control(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		for id := range c.Cursors {
			if store.ManagedLiveReachedBlock(c, id, 1) {
				t.Fatalf("crash recovery skipped Down1 replay %+v", c)
			}
		}
		out, e := r.Run(context.Background())
		if e != nil || !out.Ready || !out.Completed || !out.Recovered || calls != 0 {
			t.Fatalf("app crash replay %+v setup=%d err=%v", out, calls, e)
		}
		if e = r.Close(context.Background()); e != nil {
			t.Fatal(e)
		}
		managedInspect(t, f, func(db store.Backend) {
			rows := managedRows(t, db, "SELECT id,balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
			if len(rows) != 4 || fmt.Sprint(rows[0]["balance"]) != "2" || fmt.Sprint(rows[1]["balance"]) != "2" || fmt.Sprint(rows[2]["balance"]) != "99" || fmt.Sprint(rows[3]["balance"]) != "99" {
				t.Fatalf("app crash canonical projection %v", rows)
			}
		})
	})
}
