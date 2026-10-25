package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"reflect"
	"testing"
	"time"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type sourceLocalCall struct {
	Operation   string
	Args, After any
	Providers   []struct{ Request, Response, Error any }
	Steps       []struct {
		Operation string
		Args      any
	}
	Results        []any
	PhysicalBefore map[string]any `json:"physical_before"`
	PhysicalAfter  map[string]any `json:"physical_after"`
}
type sourceLocalCase struct {
	ID, Title      string
	SourceFile     string `json:"source_file"`
	SourceSHA      string `json:"source_sha256"`
	TestSource     string `json:"test_source"`
	Assertions     []json.RawMessage
	TypeDirectives []json.RawMessage `json:"type_error_directives"`
	Calls          []sourceLocalCall
	Targeted       bool
}

var sourceLocalCaseIDs = []string{"1337ad403051f00c5d99", "dd482eb7065250d54888", "b259864fbc99fca7585d", "6932328b1d777888e031", "35500a04eb3407d46aa8", "055e33151c56724c5459", "eb47df94258c65be3a2e", "1644fb814d5a0df1b23c", "4a23b3323e9c3992e535", "d685ca5ec1b8a49be6b3", "42b8ee0fbd39ff105e51"}

func sourceLocalValidate(cases, ledger []sourceLocalCase) error {
	original := map[string]sourceLocalCase{}
	for _, c := range ledger {
		if c.SourceFile == "packages/core/src/runtime/historical.test.ts" {
			original[c.ID] = c
		}
	}
	if len(cases) != 14 || len(original) != 14 {
		return fmt.Errorf("all fourteen historical source cases required")
	}
	targeted := map[string]bool{}
	for _, id := range sourceLocalCaseIDs {
		targeted[id] = true
	}
	seen := map[string]bool{}
	assertions, calls := 0, 0
	for _, c := range cases {
		o, ok := original[c.ID]
		if !ok || seen[c.ID] {
			return fmt.Errorf("unknown/duplicate source case")
		}
		seen[c.ID] = true
		if c.Title != o.Title || c.SourceFile != o.SourceFile || c.SourceSHA != o.SourceSHA || c.TestSource != o.TestSource || len(c.Assertions) != len(o.Assertions) || len(c.TypeDirectives) != 0 {
			return fmt.Errorf("original source metadata/body differs %s", c.ID)
		}
		for i, a := range o.Assertions {
			var left, right any
			if json.Unmarshal(a, &left) != nil || json.Unmarshal(c.Assertions[i], &right) != nil || !reflect.DeepEqual(left, right) {
				return fmt.Errorf("original assertion differs")
			}
		}
		if c.Targeted != targeted[c.ID] {
			return fmt.Errorf("source targeting differs")
		}
		if !targeted[c.ID] {
			continue
		}
		want := 1
		if c.ID == "eb47df94258c65be3a2e" || c.ID == "d685ca5ec1b8a49be6b3" || c.ID == "42b8ee0fbd39ff105e51" {
			want = 2
		}
		if len(c.Calls) != want {
			return fmt.Errorf("source generator call count differs")
		}
		for callIndex, call := range c.Calls {
			wanted := "getLocalSyncGenerator"
			for idx, id := range sourceLocalCaseIDs {
				if id == c.ID {
					if idx < 4 {
						wanted = "getLocalEventGenerator"
					} else if idx < 8 {
						wanted = "getLocalInMemoryEventGenerator"
					}
				}
			}
			if c.ID == "eb47df94258c65be3a2e" && callIndex == 0 {
				wanted = "getLocalSyncGenerator"
			}
			if call.Operation != wanted {
				return fmt.Errorf("original operation order differs")
			}
			if len(call.Results) == 0 || len(call.Steps) != len(call.Results) {
				return fmt.Errorf("complete source consumer steps required")
			}
			if len(call.PhysicalBefore) != 9 || len(call.PhysicalAfter) != 9 {
				return fmt.Errorf("complete source database snapshots required")
			}
			if call.Operation != "getLocalSyncGenerator" && call.Operation != "getLocalEventGenerator" && call.Operation != "getLocalInMemoryEventGenerator" {
				return fmt.Errorf("unexpected generator")
			}
		}
		assertions += len(c.Assertions)
		calls += len(c.Calls)
	}
	if assertions != 24 || calls != 14 {
		return fmt.Errorf("eleven cases/twenty-four assertions/fourteen generator calls required")
	}
	return nil
}
func sourceLocalOptionalObject(value any) filter.Object {
	if filter.IsUndefined(value) || value == nil {
		return nil
	}
	return value.(filter.Object)
}
func sourceLocalOptions(registry *historicalLiveRegistry, call sourceLocalCall, source *store.SourceSyncStore) (SourceLocalHistoricalOptions, filter.Object, *historicalLiveReplay, error) {
	var o SourceLocalHistoricalOptions
	raw, e := registry.restore(call.Args)
	if e != nil {
		return o, nil, nil, e
	}
	args := raw.(filter.Object)
	chain := args["chain"].(filter.Object)
	n, e := historicalLiveNumber(chain["id"])
	if e != nil {
		return o, nil, nil, e
	}
	o.ChainID = uint64(n)
	o.EventCallbacks, e = historicalLiveObjects(args["eventCallbacks"])
	if e != nil {
		return o, nil, nil, e
	}
	child, e := historicalLiveChildren(args["childAddresses"])
	if e != nil {
		return o, nil, nil, e
	}
	o.Children = NewSourceHistoricalChildren(child)
	p := args["syncProgress"].(filter.Object)
	o.Progress = &SourceSyncProgress{Start: p["start"].(filter.Object), End: sourceLocalOptionalObject(p["end"]), Current: sourceLocalOptionalObject(p["current"]), Finalized: p["finalized"].(filter.Object), ChainID: o.ChainID}
	o.CachedIntervals, e = sourceIndexPureCache(args["cachedIntervals"])
	if e != nil {
		return o, nil, nil, e
	}
	o.From, _ = args["from"].(string)
	o.To, _ = args["to"].(string)
	if !filter.IsUndefined(args["limit"]) {
		v, e := historicalLiveNumber(args["limit"])
		if e != nil {
			return o, nil, nil, e
		}
		o.Limit = int(v)
	}
	o.IsCatchup = args["isCatchup"] == true
	o.Store = source
	if opts, ok := args["options"].(filter.Object); ok {
		n, e := historicalLiveNumber(opts["factoryAddressCountThreshold"])
		if e != nil {
			return o, nil, nil, e
		}
		o.Acquisition.FactoryAddressCountThreshold = int(n)
		if opts["command"] == "dev" {
			o.TargetDuration = 2 * time.Second
		}
	}
	if !filter.IsUndefined(chain["ethGetLogsBlockRange"]) {
		n, e := historicalLiveNumber(chain["ethGetLogsBlockRange"])
		if e != nil {
			return o, nil, nil, e
		}
		v := uint64(n)
		o.Acquisition.EthGetLogsBlockRange = &v
	}
	providers := []historicalLiveProvider{}
	for _, provider := range call.Providers {
		request, ok := provider.Request.([]any)
		if !ok || len(request) < 1 {
			return o, nil, nil, fmt.Errorf("source request/context missing")
		}
		if provider.Error != nil {
			return o, nil, nil, fmt.Errorf("unexpected original provider error")
		}
		providers = append(providers, historicalLiveProvider{Request: request[0], Response: provider.Response})
	}
	replay := &historicalLiveReplay{registry: registry}
	replay.load(providers)
	o.Requester = replay
	return o, args, replay, nil
}
func sourceLocalAfter(args filter.Object, o SourceLocalHistoricalOptions) filter.Object {
	out := filter.Object{}
	for k, v := range args {
		out[k] = v
	}
	clone := func(v filter.Object) filter.Object {
		x := filter.Object{}
		for k, v := range v {
			x[k] = v
		}
		return x
	}
	out["chain"] = clone(args["chain"].(filter.Object))
	out["options"] = clone(args["options"].(filter.Object))
	callbacks := []any{}
	for _, callback := range o.EventCallbacks {
		c := clone(callback)
		c["chain"] = clone(callback["chain"].(filter.Object))
		callbacks = append(callbacks, c)
	}
	out["eventCallbacks"] = callbacks
	out["syncProgress"] = filter.Object{"start": o.Progress.Start, "end": sourceIndexOptional(o.Progress.End), "current": sourceIndexOptional(o.Progress.Current), "finalized": o.Progress.Finalized}
	out["childAddresses"] = historicalLiveChildOutput(o.Children)
	return out
}
func sourceLocalReplay(ctx context.Context, registry *historicalLiveRegistry, call sourceLocalCall, source *store.SourceSyncStore) error {
	o, args, replay, e := sourceLocalOptions(registry, call, source)
	if e != nil {
		return e
	}
	var next func() (any, error, bool)
	var stop func()
	if call.Operation == "getLocalSyncGenerator" {
		pull, end := iter.Pull2(LocalSourceSync(ctx, o))
		next = func() (any, error, bool) { v, e, ok := pull(); return v, e, ok }
		stop = end
	} else {
		seq := LocalSourceEvents(ctx, o)
		if call.Operation == "getLocalInMemoryEventGenerator" {
			seq = LocalSourceInMemoryEvents(ctx, o)
		}
		pull, end := iter.Pull2(seq)
		next = func() (any, error, bool) {
			batch, e, ok := pull()
			if e != nil || !ok {
				return nil, e, ok
			}
			events := []any{}
			for _, event := range batch.Events {
				events = append(events, event.Object())
			}
			return filter.Object{"events": events, "checkpoint": batch.Checkpoint, "blockRange": []any{batch.BlockRange[0], batch.BlockRange[1]}}, nil, true
		}
		stop = end
	}
	defer stop()
	for i, step := range call.Steps {
		if step.Operation != "next" {
			return fmt.Errorf("unhandled original consumer operation %s", step.Operation)
		}
		values, e := registry.restore(step.Args)
		if e != nil {
			return e
		}
		list, e := historicalLiveList(values)
		if e != nil || len(list) != 0 {
			return fmt.Errorf("original next argument differs")
		}
		value, e, ok := next()
		if e != nil {
			return e
		}
		if !ok {
			value = filter.Undefined
		}
		result := filter.Object{"done": !ok, "value": value}
		replay.mu.Lock()
		compareErr := registry.compare(result, call.Results[i], fmt.Sprintf("generator.result[%d]", i))
		replay.mu.Unlock()
		if compareErr != nil {
			return compareErr
		}
	}
	if _, e, ok := next(); e != nil || ok {
		return fmt.Errorf("generator unexpected extra result: %v", e)
	}
	if e := replay.finished(); e != nil {
		return e
	}
	return registry.compare(sourceLocalAfter(args, o), call.After, "generator.after")
}
func TestSourceRuntimeHistoricalOriginal(t *testing.T) {
	var fixture struct {
		Cases        []sourceLocalCase
		BackendCases map[string][]sourceLocalCase `json:"backend_cases"`
	}
	raw, e := os.ReadFile("../testdata/runtime/source-runtime-historical-live.json")
	if e != nil {
		t.Fatal(e)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if e = dec.Decode(&fixture); e != nil {
		t.Fatal(e)
	}
	var ledger struct{ Tests []sourceLocalCase }
	raw, e = os.ReadFile("../parity/test-ledger.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &ledger); e != nil {
		t.Fatal(e)
	}
	if e = sourceLocalValidate(fixture.Cases, ledger.Tests); e != nil {
		t.Fatal(e)
	}
	if len(fixture.BackendCases) != 2 {
		t.Fatal("both backends required")
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			cases := fixture.BackendCases[dialect]
			if e := sourceLocalValidate(cases, ledger.Tests); e != nil {
				t.Fatal(e)
			}
			for _, c := range cases {
				if !c.Targeted {
					continue
				}
				t.Run(c.Title, func(t *testing.T) {
					ctx := context.Background()
					if dialect == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
						t.Fatal("explicit PostgreSQL required")
					}
					backend := stagedBackend(t, ctx, dialect, "memory://")
					defer backend.Close()
					db, e := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{{Name: "local_probe", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}, fmt.Sprintf("ponder_go_local_%x", time.Now().UnixNano()), schema.Multichain)
					if e != nil {
						t.Fatal(e)
					}
					cache, e := db.EnableProtocolCache(ctx)
					if e != nil {
						t.Fatal(e)
					}
					defer func() {
						cache.Close()
						for _, ns := range []string{cache.Namespace(), db.SyncNamespace, db.Namespace} {
							if e := backend.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(ns)+" CASCADE"); e != nil {
								t.Error(e)
							}
						}
					}()
					source, e := cache.SourceSyncStore(0)
					if e != nil {
						t.Fatal(e)
					}
					registry := newSourceLiveRegistry()
					for i, call := range c.Calls {
						if e = historicalLiveComparePhysical(ctx, backend, cache, call.PhysicalBefore); e != nil {
							t.Fatalf("call%d initial: %v", i, e)
						}
						if e = sourceLocalReplay(ctx, registry, call, source); e != nil {
							t.Fatalf("call%d %s: %v", i, call.Operation, e)
						}
						if e = historicalLiveComparePhysical(ctx, backend, cache, call.PhysicalAfter); e != nil {
							t.Fatalf("call%d final: %v", i, e)
						}
					}
				})
			}
		})
	}
}

// Fail-closed metadata controls are supplemental; they add no original credit.
func TestSourceRuntimeHistoricalEvidenceCompleteness(t *testing.T) {
	var fixture struct{ Cases []sourceLocalCase }
	var ledger struct{ Tests []sourceLocalCase }
	raw, e := os.ReadFile("../testdata/runtime/source-runtime-historical-live.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile("../parity/test-ledger.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &ledger); e != nil {
		t.Fatal(e)
	}
	if e = sourceLocalValidate(fixture.Cases, ledger.Tests); e != nil {
		t.Fatal(e)
	}
	controls := map[string]func([]sourceLocalCase){
		"assertion": func(c []sourceLocalCase) { c[0].Assertions = nil },
		"body":      func(c []sourceLocalCase) { c[0].TestSource += " " },
		"operation": func(c []sourceLocalCase) { c[0].Calls[0].Operation = "getLocalSyncGenerator" },
		"consumer":  func(c []sourceLocalCase) { c[0].Calls[0].Steps = nil },
		"physical":  func(c []sourceLocalCase) { c[0].Calls[0].PhysicalAfter = nil },
		"target":    func(c []sourceLocalCase) { c[13].Targeted = true },
		"duplicate": func(c []sourceLocalCase) { c[1] = c[0] },
	}
	for name, mutate := range controls {
		t.Run(name, func(t *testing.T) {
			var copy []sourceLocalCase
			b, _ := json.Marshal(fixture.Cases)
			json.Unmarshal(b, &copy)
			mutate(copy)
			if sourceLocalValidate(copy, ledger.Tests) == nil {
				t.Fatal("altered source evidence accepted")
			}
		})
	}
}
