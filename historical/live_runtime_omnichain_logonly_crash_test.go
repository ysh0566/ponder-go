package historical

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/indexing"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type omniLogOnlyCrashSpec struct {
	RPC, Phase, Marker, Case, ParentCase, GoldenSHA256 string
	Storage                                            store.IndependentStorageOptions
}

func omniLogOnlyCrashChild(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s omniLogOnlyCrashSpec
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/runtime_omnichain_logonly_source_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(golden)) != s.GoldenSHA256 {
		t.Fatal("child source golden changed")
	}
	g := omniLogOnlyFixture(t, s.Case).Data
	tc := omniLogsNamedCase(t, g, s.Case)
	all := omniLogOnlyCompleteEvents(tc)
	calls := []string{}
	exit := func() {
		proof := map[string]any{"phase": s.Phase, "testcase": s.ParentCase, "calls": calls, "pid": os.Getpid(), "exit": 83, "golden_sha256": s.GoldenSHA256, "query_size": tc.QuerySize, "postgres_engine_stays_running": true}
		data, _ := json.Marshal(proof)
		if os.WriteFile(s.Marker, data, 0600) != nil {
			os.Exit(87)
		}
		os.Exit(83)
	}
	handlers := omniLogsHandlers(func() []omniLogsEvent { return all }, &calls, func(key string) error {
		if s.Phase == "handler" && key == tc.FailKey {
			exit()
		}
		return nil
	})
	p := &finiteRPC{server: &httptest.Server{URL: s.RPC}}
	b := omniLogOnlyBuild(t, g, p, handlers)
	epochs := []uint64{}
	o := RuntimeOptions{Historical: Options{Build: b, Owned: indexing.OwnedOptions{Storage: s.Storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}}, BuildID: "omni-logonly-exit-v16-" + tc.Name, OpenID: "logonly-exit-child", Requesters: runtimeFactory(p, &epochs), OnActivated: func(context.Context) error {
		if s.Phase == "activated" {
			exit()
		}
		return nil
	}}
	if tc.QuerySize != 12000 {
		o.Historical.QuerySize = tc.QuerySize
	}
	t.Logf("static-log child scope=%s phase=%s querySize=%d", s.ParentCase, s.Phase, tc.QuerySize)
	r, err := OpenRuntimePostgres(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Run(context.Background())
	t.Fatalf("log application child did not exit: %v", err)
}
func TestRuntimeOmnichainLogOnlyApplicationExit(t *testing.T) {
	if path := os.Getenv("PONDER_M45_OMNI_LOGONLY_CRASH_SPEC"); path != "" {
		omniLogOnlyCrashChild(t, path)
		return
	}
	for _, name := range []string{"default_failure", "default_late_failure", "empty_chain_failure", "all_empty"} {
		for _, phase := range []string{"handler", "activated"} {
			if name == "all_empty" && phase == "handler" {
				continue
			}
			t.Run("postgres/"+name+"/"+phase, func(t *testing.T) {
				continuousRoot(t)
				g := omniLogOnlyFixture(t, name).Data
				tc := omniLogsNamedCase(t, g, name)
				p, counts := omniLogsRPC(t, g)
				expected := []omniLogsEvent{}
				if phase == "handler" {
					expected = tc.Snapshots[1].Calls
				}
				calls := []string{}
				handlers := omniLogsHandlers(func() []omniLogsEvent { return expected }, &calls, nil)
				b := omniLogOnlyBuild(t, g, p, handlers)
				f := newManagedFiniteFixture(t, "postgres", b)
				omnichainGoldenStorage(&f)
				dir, err := os.MkdirTemp(os.Getenv("PONDER_M45_TEST_ROOT"), "omni-logonly-exit-"+name+"-"+phase+"-")
				if err != nil {
					t.Fatal(err)
				}
				if real, e := filepath.EvalSymlinks(dir); e != nil || !strings.HasPrefix(real, "/root/repo/") {
					t.Fatalf("invalid retained log crash root %s %v", real, e)
				}
				golden, err := os.ReadFile("testdata/runtime_omnichain_logonly_source_golden.json")
				if err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(dir, "exit-marker.json")
				spec := omniLogOnlyCrashSpec{RPC: p.server.URL, Phase: phase, Marker: marker, Case: name, ParentCase: t.Name(), GoldenSHA256: fmt.Sprintf("%x", sha256.Sum256(golden)), Storage: f.options.Owned.Storage}
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
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeOmnichainLogOnlyApplicationExit$", "-test.v")
				cmd.Env = append(os.Environ(), "PONDER_M45_OMNI_LOGONLY_CRASH_SPEC="+path)
				output, err := cmd.CombinedOutput()
				if e := os.WriteFile(filepath.Join(dir, "child.log"), output, 0600); e != nil {
					t.Fatal(e)
				}
				var exited *exec.ExitError
				if !errors.As(err, &exited) || exited.ExitCode() != 83 || ctx.Err() != nil {
					t.Fatalf("log child app exit err%v ctx%v output%s", err, ctx.Err(), output)
				}
				raw, err = os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				var proof struct {
					Phase        string   `json:"phase"`
					Testcase     string   `json:"testcase"`
					Calls        []string `json:"calls"`
					Exit         int      `json:"exit"`
					GoldenSHA256 string   `json:"golden_sha256"`
					QuerySize    int      `json:"query_size"`
					EngineAlive  bool     `json:"postgres_engine_stays_running"`
				}
				if err = json.Unmarshal(raw, &proof); err != nil || proof.Phase != phase || proof.Testcase != t.Name() || proof.Exit != 83 || proof.GoldenSHA256 != spec.GoldenSHA256 || proof.QuerySize != tc.QuerySize || !proof.EngineAlive {
					t.Fatalf("invalid log exit marker %s err%v", raw, err)
				}
				wantCalls := omniLogsKeys(omniLogOnlyCompleteEvents(tc))
				if phase == "handler" {
					wantCalls = omniLogsKeys(tc.Snapshots[0].Calls)
				}
				if !reflect.DeepEqual(proof.Calls, wantCalls) {
					t.Fatalf("wrong actual crash prefix %v want%v", proof.Calls, wantCalls)
				}
				if phase == "handler" {
					managedInspect(t, f, func(db store.Backend) {
						rows := managedRows(t, db, "SELECT id,value FROM "+schema.Quote(f.options.Owned.Storage.BusinessNamespace)+".account ORDER BY id")
						want := tc.Snapshots[0].Rows
						if len(rows) != len(want) || (len(rows) > 0 && !reflect.DeepEqual(rows, want)) {
							t.Fatalf("uncommitted log batch escaped app exit got%v want%v", rows, want)
						}
					})
				}
				epochs := []uint64{}
				o := RuntimeOptions{Historical: runtimeFixtureInput(f), BuildID: "omni-logonly-exit-v16-" + tc.Name, Requesters: runtimeFactory(p, &epochs)}
				if tc.QuerySize != 12000 {
					o.Historical.QuerySize = tc.QuerySize
				}
				for attempt := 0; attempt < 2; attempt++ {
					o.OpenID = fmt.Sprintf("logonly-exit-parent-%d", attempt)
					calls = []string{}
					if attempt > 0 {
						expected = []omniLogsEvent{}
					}
					r := runtimeOpen(t, "postgres", o)
					if attempt == 0 && phase == "handler" {
						c, e := r.Control(context.Background())
						if e != nil {
							t.Fatal(e)
						}
						for _, cp := range tc.Snapshots[0].Checkpoints {
							if c.LogicalCursors[cp.ID] != cp.Latest || c.LogicalSafe[cp.ID] != cp.Safe || c.LogicalFinality[cp.ID] != cp.Finalized {
								t.Fatalf("log exit recovery position differs %+v want%+v", c, cp)
							}
						}
					}
					out, e := r.Run(context.Background())
					if e != nil || !out.Completed || !out.Ready || !out.Recovered || !reflect.DeepEqual(calls, omniLogsKeys(expected)) {
						t.Fatalf("log application recovery result%+v calls%v want%v err%v", out, calls, omniLogsKeys(expected), e)
					}
					c, e := r.Control(context.Background())
					if e != nil {
						t.Fatal(e)
					}
					if e = r.Close(context.Background()); e != nil {
						t.Fatal(e)
					}
					assertOmniLogsSnapshot(t, f, c, tc.Snapshots[len(tc.Snapshots)-1])
				}
				counts.mu.Lock()
				defer counts.mu.Unlock()
				for _, id := range []uint64{1, 2} {
					if counts.Logs[id] == 0 || counts.Full[id] == 0 {
						t.Fatalf("crash path missed actual log/full-block acquisition chain%d %+v", id, counts)
					}
				}
			})
		}
	}
}

func omniLogOnlyFixture(t *testing.T, name string) omniLogOnlyCase {
	t.Helper()
	for _, f := range loadOmniLogOnlyGolden(t) {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("missing log-only case %s", name)
	return omniLogOnlyCase{}
}
func omniLogOnlyCompleteEvents(tc omniLogsCase) []omniLogsEvent {
	byKey := map[string]omniLogsEvent{}
	for _, snapshot := range tc.Snapshots {
		for _, event := range snapshot.Calls {
			byKey[event.Key] = event
		}
	}
	last := tc.Snapshots[len(tc.Snapshots)-1]
	out := []omniLogsEvent{}
	if len(last.Rows) == 0 {
		return out
	}
	for _, key := range strings.Split(fmt.Sprint(last.Rows[0]["value"]), "|") {
		out = append(out, byKey[key])
	}
	return out
}
