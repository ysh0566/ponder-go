package historical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/store"
)

type sparseCrashSpec struct {
	Query         int
	Phase, Marker string
	Storage       store.IndependentStorageOptions
}

func TestRuntimeOmnichainSparseApplicationExit(t *testing.T) {
	const specEnv = "PONDER_SPARSE_EXIT_SPEC"
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	continuousRoot(t)
	specPath := os.Getenv(specEnv)
	run := func(t *testing.T, spec sparseCrashSpec) {
		var calls []string
		want := sparseExpectedCalls(2)
		failAt := fmt.Sprintf("2:%d:1:A", sparseDistant)
		exit := func() {
			body, err := json.Marshal(calls)
			if err != nil || os.WriteFile(spec.Marker, body, 0600) != nil {
				os.Exit(89)
			}
			os.Exit(83)
		}
		handlers := sparseHandlers(&calls, func(key string) error {
			if specPath != "" && spec.Phase == "handler" && key == failAt {
				exit()
			}
			return nil
		})
		g, p := sparseLogsRPC(t, 2)
		build := omniLogOnlyRangeBuild(t, g, p, handlers, sparseEnd)
		f := newManagedFiniteFixture(t, "postgres", build)
		omnichainGoldenStorage(&f)
		if specPath != "" {
			f.options.Owned.Storage = spec.Storage
		}
		var epochs []uint64
		opts := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "sparse-app-exit", Requesters: runtimeFactory(p, &epochs)}
		opts.Historical.QuerySize = spec.Query
		if specPath != "" {
			opts.OpenID = "sparse-child"
			opts.OnActivated = func(context.Context) error {
				if spec.Phase == "activated" {
					exit()
				}
				return nil
			}
			r := runtimeOpen(t, "postgres", opts)
			seedSparseSource(t, r, g)
			_, err := r.Run(context.Background())
			t.Fatalf("child survived %s: %v", spec.Phase, err)
		}
		dir := newRuntimeExitDirectory(t, "sparse-exit-")
		spec.Marker = filepath.Join(dir, "calls.json")
		spec.Storage = f.options.Owned.Storage
		raw, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "spec.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeOmnichainSparseApplicationExit$")
		cmd.Env = append(os.Environ(), specEnv+"="+path)
		output, err := cmd.CombinedOutput()
		var child *exec.ExitError
		if !errors.As(err, &child) || child.ExitCode() != 83 || ctx.Err() != nil {
			t.Fatalf("child exit: %v %s", err, output)
		}
		raw, err = os.ReadFile(spec.Marker)
		if err != nil {
			t.Fatal(err)
		}
		var actual []string
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatal(err)
		}
		called, retained := len(want), len(want)
		if spec.Phase == "handler" {
			called = slices.Index(want, failAt) + 1
			retained = 0
			if spec.Query == 6 {
				retained = 16
			}
		}
		if !slices.Equal(actual, want[:called]) {
			t.Fatalf("child calls %v want %v", actual, want[:called])
		}
		assertSparseRows(t, f, want[:retained])
		opts.OpenID = "sparse-reopen"
		r := runtimeOpen(t, "postgres", opts)
		out, err := r.Run(context.Background())
		if err != nil || !out.Ready || !out.Completed || !out.Recovered || !slices.Equal(calls, want[retained:]) {
			t.Fatalf("cold recovery %+v %v calls %v", out, err, calls)
		}
		c, err := r.Control(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for id := uint64(1); id <= 2; id++ {
			cp := checkpoint.MustEncode(checkpoint.Block(100+sparseEnd, id, sparseEnd, true))
			if c.LogicalSafe[id] != cp || c.LogicalCursors[id] != cp || c.LogicalFinality[id] != cp {
				t.Fatalf("chain %d positions after exit %+v", id, c)
			}
		}
		if err := r.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSparseRows(t, f, want)
	}
	if specPath != "" {
		raw, err := os.ReadFile(specPath)
		if err != nil {
			t.Fatal(err)
		}
		var spec sparseCrashSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		run(t, spec)
		return
	}
	for _, spec := range []sparseCrashSpec{{Query: 12000, Phase: "handler"}, {Query: 6, Phase: "handler"}, {Query: 12000, Phase: "activated"}} {
		t.Run(fmt.Sprintf("%d/%s", spec.Query, spec.Phase), func(t *testing.T) { run(t, spec) })
	}
}
