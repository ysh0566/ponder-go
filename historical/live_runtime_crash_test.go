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
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"strings"
	"testing"
	"time"
)

type continuousCrashSpec struct {
	RPC, Phase, Marker string
	Storage            store.IndependentStorageOptions
}

func continuousCrashChild(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec continuousCrashSpec
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	p := &finiteRPC{server: &httptest.Server{URL: spec.RPC}}
	b := runtimeBuild(t, p, 4, 1, indexing.Handler(finiteIncrement))
	for _, c := range b.Executable().Chains {
		c["reorgWindow"] = uint64(600)
	}
	epochs := []uint64{}
	var r *RuntimeRunner
	forked := false
	o := RuntimeOptions{Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: spec.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "continuous-crash-v1", OpenID: "crash-child-1", Requesters: runtimeFactory(p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond, OnDurableReorg: func(ctx context.Context, e store.ManagedLiveDomainStage) error {
		if e.Phase != spec.Phase {
			return nil
		}
		if ready, err := r.Ready(ctx); err != nil || ready {
			os.Exit(86)
		}
		marker, _ := json.Marshal(map[string]any{"phase": e.Phase, "operation": e.OperationID, "pid": os.Getpid(), "exit": 83, "postgres_engine_stays_running": true})
		if err := os.WriteFile(spec.Marker, marker, 0600); err != nil {
			os.Exit(87)
		}
		os.Exit(83)
		return nil
	}, OnProgress: func(ctx context.Context, e RuntimeLiveProgress) error {
		if e.Type != "block" || forked {
			return nil
		}
		c, err := r.Control(ctx)
		if err != nil {
			return err
		}
		both := true
		for _, h := range c.Cursors {
			cp, _ := checkpoint.Decode(h.Checkpoint)
			both = both && cp.BlockNumber >= 3
		}
		if both {
			forked = true
			var yes bool
			client := rpc.New(spec.RPC + "/1")
			client.Retries = 0
			client.RequestsPerSecond = 0
			return client.Call(ctx, "ponder_test_fork", []any{}, &yes)
		}
		return nil
	}}}
	r, err = OpenRuntimePostgres(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background())
	t.Fatalf("child did not abruptly exit at durable %s: %v", spec.Phase, err)
}

// OS.Exit bypasses runtime/owner defers. PostgreSQL itself is never stopped or
// killed: these are application-process crash boundaries, not engine crashes.
func TestRuntimeContinuousCrash(t *testing.T) {
	if path := os.Getenv("PONDER_M45_CONTINUOUS_CRASH_SPEC"); path != "" {
		continuousCrashChild(t, path)
		return
	}
	for _, phase := range []string{"prepare", "business", "ack"} {
		t.Run("postgres/"+phase, func(t *testing.T) {
			continuousRoot(t)
			s := newContinuousRPC(t, 3)
			b := runtimeBuild(t, s.p, 4, 1, indexing.Handler(finiteIncrement))
			for _, c := range b.Executable().Chains {
				c["reorgWindow"] = uint64(600)
			}
			f := newManagedFiniteFixture(t, "postgres", b)
			root := os.Getenv("PONDER_M45_TEST_ROOT")
			dir, err := os.MkdirTemp(root, "runtime-app-crash-"+phase+"-")
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil || !strings.HasPrefix(resolved, "/root/repo/") {
				t.Fatalf("child evidence escaped /root/repo: %s %v", resolved, err)
			}
			marker := filepath.Join(dir, "exit-marker.json")
			spec := continuousCrashSpec{RPC: s.p.server.URL, Phase: phase, Marker: marker, Storage: f.options.Owned.Storage}
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "child-spec.json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeContinuousCrash$", "-test.v")
			command.Env = append(os.Environ(), "PONDER_M45_CONTINUOUS_CRASH_SPEC="+path)
			output, err := command.CombinedOutput()
			if e := os.WriteFile(filepath.Join(dir, "child.log"), output, 0600); e != nil {
				t.Fatal(e)
			}
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 83 || ctx.Err() != nil {
				t.Fatalf("abrupt app exit=%v ctx=%v output=%s", err, ctx.Err(), output)
			}
			proof, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			var marked struct {
				Phase, Operation string
				Exit             int
			}
			if err = json.Unmarshal(proof, &marked); err != nil || marked.Phase != phase || marked.Exit != 83 || marked.Operation == "" {
				t.Fatalf("missing native boundary marker=%s err=%v", proof, err)
			}
			epochs := []uint64{}
			o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "continuous-crash-v1", OpenID: "crash-parent-2", Requesters: runtimeFactory(s.p, &epochs), Live: &RuntimeLiveOptions{PollingInterval: time.Millisecond}}
			r := runtimeOpen(t, "postgres", o)
			c, err := r.Control(context.Background())
			if err != nil || c.Recoveries != 1 || c.Serving || len(c.ReplayTargets) == 0 {
				t.Fatalf("actual abrupt-exit recovery=%+v err=%v", c, err)
			}
			out, err := r.Run(context.Background())
			if err != nil || !out.Completed || !out.Recovered || !out.Ready {
				t.Fatalf("actual crash replay=%+v err=%v", out, err)
			}
			evidence, err := r.domains.DomainEvidence(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(evidence), marked.Operation) {
				t.Fatalf("crashed durable intent not retained: %s", evidence)
			}
			if err = r.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			managedInspect(t, f, func(db store.Backend) {
				rows := managedRows(t, db, "SELECT balance::text AS balance FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
				if len(rows) != 2 || fmt.Sprint(rows[0]["balance"]) != "4" || fmt.Sprint(rows[1]["balance"]) != "4" {
					t.Fatalf("crash duplicated/lost committed rows: %v", rows)
				}
			})
		})
	}
}
