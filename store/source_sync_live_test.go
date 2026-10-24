package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/schema"
)

type sourceLiveQuery struct {
	SQL  string `json:"sql"`
	Args []any  `json:"args"`
}
type sourceLiveBackend struct {
	Backend
	mu      sync.Mutex
	queries []sourceLiveQuery
}

func (b *sourceLiveBackend) record(sql string, args []any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queries = append(b.queries, sourceLiveQuery{sql, append([]any{}, args...)})
}
func (b *sourceLiveBackend) Query(ctx context.Context, sql string, args ...any) (Result, error) {
	b.record(sql, args)
	return b.Backend.Query(ctx, sql, args...)
}
func (b *sourceLiveBackend) Exec(ctx context.Context, sql string, args ...any) error {
	b.record(sql, args)
	return b.Backend.Exec(ctx, sql, args...)
}
func (b *sourceLiveBackend) Begin(ctx context.Context) (Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &sourceLiveTx{Transaction: tx, backend: b}, nil
}

type sourceLiveTx struct {
	Transaction
	backend *sourceLiveBackend
}

func (tx *sourceLiveTx) Query(ctx context.Context, sql string, args ...any) (Result, error) {
	tx.backend.record(sql, args)
	return tx.Transaction.Query(ctx, sql, args...)
}
func (tx *sourceLiveTx) Exec(ctx context.Context, sql string, args ...any) error {
	tx.backend.record(sql, args)
	return tx.Transaction.Exec(ctx, sql, args...)
}

type sourceLiveCase struct {
	ID, Title, SourceSHA, SourceBody string
	Endpoint                         string
	TypesDone                        bool
	TypeRuns                         []map[string]any
	Assertions                       any
	Store                            *SourceSyncStore
	Registry                         *sourceLiveRegistry
	Calls, Seeds, Sequence           int
	Complete                         bool
	Errors                           []string
	Queries                          []sourceLiveQuery
}
type sourceLiveCallRequest struct {
	CaseID    string         `json:"case_id"`
	Operation string         `json:"operation"`
	Sequence  int            `json:"sequence"`
	Args      any            `json:"args"`
	Result    any            `json:"result"`
	Physical  map[string]any `json:"physical_rows"`
	SQL       string         `json:"sql"`
	Params    any            `json:"params"`
}

func sourceLiveReadJSON(file string, out any) error {
	raw, e := os.ReadFile(file)
	if e != nil {
		return e
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}
func sourceLiveQueryRange(state *sourceLiveCase) error {
	for _, table := range []string{"logs", "traces"} {
		var found *sourceLiveQuery
		needle := "from " + state.Store.cache.table(table)
		for i := range state.Queries {
			if strings.Contains(strings.ToLower(state.Queries[i].SQL), needle) {
				found = &state.Queries[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("original SQL assertion: native %s query missing", table)
		}
		if strings.Count(found.SQL, `"block_number" >=`) != 1 || strings.Count(found.SQL, `"block_number" <=`) != 1 {
			return fmt.Errorf("original SQL assertion: %s query range duplicated/missing: %s", table, found.SQL)
		}
		want := []string{"10", "90"}
		if table == "traces" {
			want = []string{"30", "70"}
		}
		for _, value := range want {
			present := false
			for _, arg := range found.Args {
				if fmt.Sprint(arg) == value {
					present = true
				}
			}
			if !present {
				return fmt.Errorf("original SQL assertion: %s bound%s missing: %v", table, value, found.Args)
			}
		}
	}
	return nil
}

// Full original file and real source setup run unchanged. Each actual native
// method result and all nine physical tables are compared while original source
// results continue to serve its assertions. Mapping/type/runtime acceptance is
// separately gated; this test alone does not grant source credit.
func TestUpstreamSourceSyncStore(t *testing.T) {
	root, e := filepath.Abs("..")
	if e != nil {
		t.Fatal(e)
	}
	var ledger struct {
		Tests []map[string]any `json:"tests"`
	}
	if e = sourceLiveReadJSON(filepath.Join(root, "parity/test-ledger.json"), &ledger); e != nil {
		t.Fatal(e)
	}
	records := []map[string]any{}
	for _, record := range ledger.Tests {
		if record["source_file"] == "packages/core/src/sync-store/index.test.ts" {
			records = append(records, record)
		}
	}
	if len(records) != 38 {
		t.Fatal("missing all38 original source records")
	}
	source, e := os.ReadFile(filepath.Join(root, "../ponder-upstream/packages/core/src/sync-store/index.test.ts"))
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(source)
	sourceHash := hex.EncodeToString(sum[:])
	controls, e := sourceLiveLoadTypes(root, sourceHash)
	if e != nil {
		t.Fatal(e)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			raw := sourceBackend(t, ctx, dialect)
			backend := &sourceLiveBackend{Backend: raw}
			states := map[string]*sourceLiveCase{}
			order := []string{}
			for _, record := range records {
				id := record["id"].(string)
				if record["source_sha256"] != sourceHash {
					t.Fatal("original source hash changed")
				}
				states[id] = &sourceLiveCase{ID: id, Title: record["title"].(string), SourceSHA: sourceHash, SourceBody: record["test_source"].(string), Assertions: record["assertions"], Registry: newSourceLiveRegistry(), Errors: []string{}}
				order = append(order, id)
			}
			var mu sync.Mutex
			var active *sourceLiveCase
			typesStarted, typesFinished, backendClosedBeforeTypes := false, false, false
			typesRun := map[string]bool{}
			directory, e := os.MkdirTemp(filepath.Join(root, "parity/runs/sync-store-m32"), "live-")
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("Live source sync evidence: %s", directory)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				fail := func(err error) {
					if active != nil {
						active.Errors = append(active.Errors, err.Error())
					}
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
				defer func() {
					if value := recover(); value != nil {
						fail(fmt.Errorf("native bridge panic: %v", value))
					}
				}()
				decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
				decoder.UseNumber()
				if r.URL.Path == "/types-start" {
					var input struct {
						CaseIDs []string `json:"case_ids"`
					}
					if e := decoder.Decode(&input); e != nil {
						fail(e)
						return
					}
					if typesStarted || !reflect.DeepEqual(input.CaseIDs, order) {
						fail(fmt.Errorf("all original cases must precede native type controls"))
						return
					}
					for _, id := range order {
						state := states[id]
						if !state.Complete || state.Calls == 0 || len(state.Errors) != 0 {
							fail(fmt.Errorf("original comparison incomplete before type controls: %s", id))
							return
						}
					}
					// A standalone positive program opens a real database. Release the
					// comparison process before it starts, so PGlite does not require
					// three concurrent WASM instances in the cloud executor.
					if e := backend.Close(); e != nil {
						fail(e)
						return
					}
					backendClosedBeforeTypes, typesStarted = true, true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == "/types-run" {
					var input struct {
						SourceID string `json:"source_id"`
						Index    int    `json:"directive_index"`
					}
					if e := decoder.Decode(&input); e != nil {
						fail(e)
						return
					}
					key := fmt.Sprintf("%s/%d", input.SourceID, input.Index)
					if !typesStarted || typesFinished || !backendClosedBeforeTypes || typesRun[key] {
						fail(fmt.Errorf("invalid native type control phase: %s", key))
						return
					}
					var selected []sourceLiveTypeControl
					for _, control := range controls {
						if control.SourceID == input.SourceID && control.Index == input.Index {
							selected = append(selected, control)
						}
					}
					if len(selected) != 1 {
						fail(fmt.Errorf("unknown native type control: %s", key))
						return
					}
					state := states[input.SourceID]
					active = state
					executions, err := sourceLiveRunTypes(ctx, root, dialect, state.Endpoint, state.ID, selected)
					for _, execution := range executions {
						execution["phase"] = "after_all_original_comparisons"
						execution["native_backend_closed"] = true
					}
					state.TypeRuns = append(state.TypeRuns, executions...)
					if err != nil {
						fail(err)
						return
					}
					typesRun[key] = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == "/types-finish" {
					if !typesStarted || typesFinished || len(typesRun) != len(controls) {
						fail(fmt.Errorf("all nine actual native type controls required"))
						return
					}
					for _, id := range order {
						states[id].TypesDone = true
					}
					typesFinished = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == "/start" {
					var input struct {
						CaseID   string `json:"case_id"`
						Endpoint string `json:"endpoint"`
					}
					if e := decoder.Decode(&input); e != nil {
						fail(e)
						return
					}
					state := states[input.CaseID]
					if state == nil || state.Store != nil {
						fail(fmt.Errorf("unknown/repeated source case"))
						return
					}
					if active != nil && !active.Complete {
						fail(fmt.Errorf("previous original case incomplete"))
						return
					}
					endpoint, err := url.Parse(input.Endpoint)
					if err != nil || endpoint.Scheme != "http" || (endpoint.Hostname() != "127.0.0.1" && endpoint.Hostname() != "localhost") {
						fail(fmt.Errorf("original live loopback endpoint required"))
						return
					}
					state.Endpoint = input.Endpoint
					active = state
					backend.mu.Lock()
					backend.queries = nil
					backend.mu.Unlock()
					store, e := New(ctx, backend, sourceActionsModel(), fmt.Sprintf("ponder_go_srcsync_%x", time.Now().UnixNano()), schema.Multichain)
					if e != nil {
						fail(e)
						return
					}
					cache, e := store.EnableProtocolCache(ctx)
					if e != nil {
						fail(e)
						return
					}
					state.Store, e = cache.SourceSyncStore(0)
					if e != nil {
						fail(e)
						return
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if active == nil {
					fail(fmt.Errorf("native source case not started"))
					return
				}
				if r.URL.Path == "/case" {
					var input map[string]any
					if e := decoder.Decode(&input); e != nil {
						fail(e)
						return
					}
					if input["id"] != active.ID || input["source_sha256"] != active.SourceSHA || input["test_source"] != active.SourceBody || !reflect.DeepEqual(input["assertions"], active.Assertions) {
						fail(fmt.Errorf("original source body/assertion context changed"))
						return
					}
					if active.Calls == 0 {
						fail(fmt.Errorf("original case has no real native calls"))
						return
					}
					backend.mu.Lock()
					active.Queries = append([]sourceLiveQuery{}, backend.queries...)
					backend.mu.Unlock()
					if strings.HasPrefix(active.Title, "getEventData() applies one block range") {
						if e := sourceLiveQueryRange(active); e != nil {
							fail(e)
							return
						}
					}
					active.Complete = true
					if e := active.Store.cache.Close(); e != nil {
						fail(e)
						return
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				var call sourceLiveCallRequest
				if e := decoder.Decode(&call); e != nil {
					fail(e)
					return
				}
				if call.CaseID != active.ID || call.Sequence != active.Sequence {
					fail(fmt.Errorf("source seed/method ordering changed"))
					return
				}
				active.Sequence++
				switch r.URL.Path {
				case "/seed-sql":
					value, e := active.Registry.restore(call.Params)
					if e != nil {
						fail(e)
						return
					}
					args, e := sourceLiveList(value)
					if e != nil {
						fail(e)
						return
					}
					if e = sourceLiveSeedSQL(ctx, active.Store.cache, call.SQL, args); e != nil {
						fail(e)
						return
					}
					active.Seeds++
				case "/call":
					value, e := active.Registry.restore(call.Args)
					if e != nil {
						fail(e)
						return
					}
					args, e := sourceLiveObject(value)
					if e != nil {
						fail(e)
						return
					}
					result, e := sourceLiveInvoke(ctx, active.Store, call.Operation, args)
					if e != nil {
						fail(fmt.Errorf("%s native method: %w", call.Operation, e))
						return
					}
					if e = active.Registry.compare(sourceLiveOutput(result), call.Result, call.Operation); e != nil {
						fail(e)
						return
					}
					if call.Physical != nil {
						rows, e := sourceLivePhysical(ctx, active.Store.cache)
						if e != nil {
							fail(e)
							return
						}
						if e = sourceLiveComparePhysical(rows, call.Physical); e != nil {
							fail(fmt.Errorf("%s: %w", call.Operation, e))
							return
						}
					}
					active.Calls++
				default:
					fail(fmt.Errorf("unknown native source bridge route"))
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			command := exec.CommandContext(ctx, "node", filepath.Join(root, "tools/source-sync-original.mjs"), "--backend="+dialect, "--output="+directory)
			command.Dir = root
			command.Env = append(os.Environ(), "PONDER_SOURCE_SYNC_ENDPOINT="+server.URL, "PONDER_PORT_ROOT="+root)
			output, e := command.CombinedOutput()
			if writeErr := os.WriteFile(filepath.Join(directory, "parent-output.txt"), output, 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			mu.Lock()
			report := map[string]any{"state": "Actual full original/native live calls; no mapping/type/runtime acceptance implied", "backend": dialect, "cases": []any{}, "original_exit_error": fmt.Sprint(e)}
			report["native_backend_closed_before_types"] = backendClosedBeforeTypes
			report["types_finished"] = typesFinished
			report["type_execution_phase"] = "after_all_original_comparisons_before_original_anvil_teardown"
			cases := []any{}
			for _, id := range order {
				state := states[id]
				cases = append(cases, map[string]any{"id": id, "title": state.Title, "source_sha256": state.SourceSHA, "calls": state.Calls, "setup_seeds": state.Seeds, "sequence": state.Sequence, "complete": state.Complete, "errors": state.Errors, "native_queries": state.Queries, "native_type_executions": state.TypeRuns})
			}
			report["cases"] = cases
			encoded, _ := json.MarshalIndent(report, "", "  ")
			writeErr := os.WriteFile(filepath.Join(directory, "native.json"), append(encoded, '\n'), 0600)
			mu.Unlock()
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			if e != nil {
				t.Fatalf("complete original live bridge failed: %v\n%s\nEvidence:%s", e, output, directory)
			}
			for _, id := range order {
				state := states[id]
				t.Run(id, func(t *testing.T) {
					expected := 0
					for _, control := range controls {
						if control.SourceID == id {
							expected++
						}
					}
					if !state.Complete || len(state.Errors) > 0 || state.Calls == 0 || !state.TypesDone || len(state.TypeRuns) != expected {
						t.Fatalf("original case/native proof incomplete: %+v", state)
					}
				})
			}
		})
	}
}
