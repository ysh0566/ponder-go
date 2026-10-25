package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math/big"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type sourceMultichainCall struct {
	Operation   string
	Name        string
	Args, After any
	Providers   []struct {
		ChainID                  uint64 `json:"chainId"`
		Request, Response, Error any
	}
	Steps []struct {
		Operation string
		Args      any
	}
	Results        []any
	Clocks         []json.Number
	PhysicalBefore map[string]any `json:"physical_before"`
	PhysicalAfter  map[string]any `json:"physical_after"`
}
type sourceMultichainCase struct {
	ID, Title      string
	SourceFile     string `json:"source_file"`
	SourceSHA      string `json:"source_sha256"`
	TestSource     string `json:"test_source"`
	Assertions     []json.RawMessage
	TypeDirectives []json.RawMessage `json:"type_error_directives"`
	Calls          []sourceMultichainCall
	Targeted       bool
}

var sourceMultichainIDs = []string{"d1f4e702f072c3b5eae1", "983c1f8526efc67c34aa", "a2f204a8ab7598691e12"}

func sourceMultichainValidate(cases, ledger []sourceMultichainCase) error {
	originals := map[string]sourceMultichainCase{}
	for _, c := range ledger {
		if c.SourceFile == "packages/core/src/runtime/historical.test.ts" {
			originals[c.ID] = c
		}
	}
	if len(cases) != 14 || len(originals) != 14 {
		return fmt.Errorf("all fourteen original historical cases required")
	}
	selected := map[string]bool{}
	for _, id := range sourceMultichainIDs {
		selected[id] = true
	}
	seen := map[string]bool{}
	assertions, calls := 0, 0
	for _, c := range cases {
		o, ok := originals[c.ID]
		if !ok || seen[c.ID] {
			return fmt.Errorf("unknown/duplicate historical case")
		}
		seen[c.ID] = true
		if c.Title != o.Title || c.SourceFile != o.SourceFile || c.SourceSHA != o.SourceSHA || c.TestSource != o.TestSource || len(c.Assertions) != len(o.Assertions) || len(c.TypeDirectives) != 0 || len(o.TypeDirectives) != 0 {
			return fmt.Errorf("original metadata/body mismatch %s", c.ID)
		}
		for i, a := range c.Assertions {
			var left, right any
			if json.Unmarshal(a, &left) != nil || json.Unmarshal(o.Assertions[i], &right) != nil || !reflect.DeepEqual(left, right) {
				return fmt.Errorf("original assertion mismatch %s", c.ID)
			}
		}
		if c.Targeted != selected[c.ID] {
			return fmt.Errorf("target set mismatch")
		}
		if !c.Targeted {
			continue
		}
		if len(c.Calls) != 1 {
			return fmt.Errorf("exact original coordinator call required")
		}
		call := c.Calls[0]
		if call.Operation != "getHistoricalEventsMultichain" {
			return fmt.Errorf("original coordinator operation mismatch")
		}
		if len(call.Steps) == 0 || len(call.Steps) != len(call.Results) {
			return fmt.Errorf("complete consumer steps/results required")
		}
		if len(call.PhysicalBefore) != 9 || len(call.PhysicalAfter) != 9 {
			return fmt.Errorf("all nine database snapshots required")
		}
		if len(call.Clocks) < 2 {
			return fmt.Errorf("original coordinator clock observations required")
		}
		assertions += len(c.Assertions)
		calls += len(c.Calls)
	}
	if assertions != 5 || calls != 3 {
		return fmt.Errorf("three cases/five assertions/three calls required")
	}
	return nil
}

// Functions in original chain metadata/callbacks are opaque input descriptors,
// never evaluated by historical extraction. The source observer records only
// the function name, not an identity or body; this token preserves exactly that
// limited observation and makes no executable-function compatibility claim.
type sourceMultichainOpaqueFunction struct{ Name string }

func sourceMultichainWire(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = sourceMultichainWire(x)
		}
		return out
	case map[string]any:
		if name, ok := v["$function"].(string); ok && len(v) == 1 {
			return sourceMultichainOpaqueFunction{Name: name}
		}
		out := map[string]any{}
		for k, x := range v {
			out[k] = sourceMultichainWire(x)
		}
		return out
	default:
		return value
	}
}

// All chain replayers share one registry lock; coordinator prefetch can issue
// calls on different chains concurrently while the consumer checks identities.
type sourceMultichainRequester struct {
	mu                    *sync.Mutex
	replay                *historicalLiveReplay
	delayed               bool
	chainID               uint64
	latestTimeline        *[]any
	gate                  *sourceMultichainProbeGate
	providerError         error
	violation             error
	errorMethod, errorTag string
}

func (r *sourceMultichainRequester) Call(ctx context.Context, method string, params, out any) error {
	return <-r.BeginCall(ctx, method, params, out)
}

// Admit the captured request synchronously; only its controlled completion may
// be delayed. This preserves the source invocation order without goroutine admission.
func (r *sourceMultichainRequester) BeginCall(ctx context.Context, method string, params, out any) <-chan error {
	r.mu.Lock()
	latest, numeric := false, false
	tag := ""
	if p, ok := params.([]any); ok && len(p) > 0 {
		tag, _ = p[0].(string)
		latest = method == "eth_getBlockByNumber" && tag == "latest"
		numeric = method == "eth_getBlockByNumber" && strings.HasPrefix(tag, "0x")
	}
	if latest && r.gate != nil && r.gate.errorHeld != 0 {
		r.gate.special("latest_begin", r.chainID)
	}
	if latest && r.latestTimeline != nil {
		*r.latestTimeline = append(*r.latestTimeline, map[string]any{"kind": "begin", "chainId": r.chainID})
	}
	err := r.replay.Call(ctx, method, params, out)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && r.violation == nil {
		r.violation = err
	}
	if r.gate != nil && r.gate.memory && tag == "0x0" {
		if method == "eth_getBlockByNumber" {
			r.gate.memoryEvent("block_begin")
		} else if method == "debug_traceBlockByNumber" {
			r.gate.memoryEvent("trace_begin")
		}
	}
	if err == nil && method == r.errorMethod && tag == r.errorTag && r.providerError != nil {
		err = r.providerError
		if r.gate != nil && r.gate.errorHeld != 0 {
			if r.gate.memory {
				r.gate.memoryEvent("trace_error")
			} else {
				r.gate.special("latest_error", r.chainID)
			}
		}
	}
	first := numeric && !r.delayed
	if numeric {
		r.delayed = true
	}
	finish := func() {
		r.mu.Lock()
		if latest && r.latestTimeline != nil {
			*r.latestTimeline = append(*r.latestTimeline, map[string]any{"kind": "finish", "chainId": r.chainID})
		}
		r.mu.Unlock()
		if latest && r.gate != nil {
			r.gate.response(r.chainID)
		}
	}
	r.mu.Unlock()
	done := make(chan error, 1)
	memoryHeld := r.gate != nil && r.gate.memory && numeric && tag == "0x0"
	gated := r.gate != nil && (memoryHeld || (first && r.gate.firstChain == r.chainID) || (latest && r.gate.latestChain == r.chainID) || (latest && r.gate.errorHeld == r.chainID))
	if !gated {
		finish()
		done <- err
		return done
	}
	r.gate.workers.Add(1)
	go func() {
		defer r.gate.workers.Done()
		var e error
		if memoryHeld || (latest && r.gate.errorHeld == r.chainID) {
			e = r.gate.wait(ctx, func() bool { return r.gate.errorReleased })
		} else if latest {
			e = r.gate.wait(ctx, func() bool { return r.gate.responses[r.gate.latestAfter] })
		} else {
			r.gate.event("block_response_held", r.chainID, "throughBlock", float64(r.gate.firstThrough))
			e = r.gate.wait(ctx, func() bool { return r.gate.through(r.gate.firstAfter, float64(r.gate.firstThrough)) })
			if e == nil {
				r.gate.event("block_response_released", r.chainID, "throughBlock", float64(r.gate.firstThrough))
			}
		}
		if memoryHeld || (latest && r.gate.errorHeld == r.chainID) {
			kind := "latest_released"
			if e != nil {
				kind = "latest_cancelled"
			}
			if memoryHeld {
				kind = strings.Replace(kind, "latest_", "block_", 1)
				r.gate.memoryEvent(kind)
			} else {
				r.gate.special(kind, r.chainID)
			}
		}
		if e != nil {
			done <- e
			return
		}
		finish()
		done <- err
	}()
	return done
}

// A validation failure remains fatal even if the implementation ignores the
// returned provider error and later consumes every expected request.
func (r *sourceMultichainRequester) finished() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.violation != nil {
		return r.violation
	}
	return r.replay.finished()
}

type sourceMultichainReplayState struct {
	registry   *historicalLiveRegistry
	mu         sync.Mutex
	args       filter.Object
	options    SourceHistoricalCoordinatorOptions
	replays    map[uint64]*historicalLiveReplay
	clockMu    sync.Mutex
	clocks     []json.Number
	clockIndex int
	clockError error
}

func newSourceMultichainReplay(registry *historicalLiveRegistry, call sourceMultichainCall, source *store.SourceSyncStore) (*sourceMultichainReplayState, error) {
	s := &sourceMultichainReplayState{registry: registry, replays: map[uint64]*historicalLiveReplay{}, clocks: call.Clocks}
	raw, e := registry.restore(sourceMultichainWire(call.Args))
	if e != nil {
		return nil, e
	}
	s.args, e = historicalLiveObject(raw)
	if e != nil {
		return nil, e
	}
	buildChains, e := historicalLiveObjects(s.args["chains"])
	if e != nil {
		return nil, e
	}
	callbackLists, e := historicalLiveList(s.args["eventCallbacks"])
	if e != nil || len(callbackLists) != len(buildChains) {
		return nil, fmt.Errorf("original build callbacks/chains differ")
	}
	options, e := historicalLiveObject(s.args["options"])
	if e != nil {
		return nil, e
	}
	query, e := historicalLiveUint(options["syncEventsQuerySize"])
	if e != nil {
		return nil, e
	}
	threshold, e := historicalLiveUint(options["factoryAddressCountThreshold"])
	if e != nil {
		return nil, e
	}
	s.options.QuerySize = int(query)
	callbackByChain := map[uint64][]filter.Object{}
	buildIDs := []uint64{}
	for i, c := range buildChains {
		id, e := historicalLiveUint(c["id"])
		if e != nil {
			return nil, e
		}
		callbacks, e := historicalLiveObjects(callbackLists[i])
		if e != nil {
			return nil, e
		}
		if _, exists := callbackByChain[id]; exists {
			return nil, fmt.Errorf("duplicate original build chain")
		}
		callbackByChain[id] = callbacks
		buildIDs = append(buildIDs, id)
	}
	providers := map[uint64][]historicalLiveProvider{}
	providerErrors := map[uint64]error{}
	errorMethods, errorTags := map[uint64]string{}, map[uint64]string{}
	for _, p := range call.Providers {
		if _, ok := callbackByChain[p.ChainID]; !ok {
			return nil, fmt.Errorf("provider references unknown chain")
		}
		request, ok := p.Request.([]any)
		if !ok || len(request) < 1 {
			return nil, fmt.Errorf("complete raw provider request required")
		}
		if p.Error != nil {
			rawRequest, ok := sourceMultichainPhysical(request[0]).(map[string]any)
			if !ok {
				return nil, fmt.Errorf("provider failure request missing")
			}
			params, ok := rawRequest["params"].([]any)
			if !ok || len(params) == 0 {
				return nil, fmt.Errorf("error request params required")
			}
			expectedMessage := ""
			method, _ := rawRequest["method"].(string)
			tag, _ := params[0].(string)
			switch call.Name {
			case "latest-first-error":
				if p.ChainID != 2 || method != "eth_getBlockByNumber" || tag != "latest" {
					return nil, fmt.Errorf("unexpected latest failure request")
				}
				expectedMessage = "M37 injected latest failure"
			case "memory-block-trace-first-error":
				if p.ChainID != 1 || method != "debug_traceBlockByNumber" || tag != "0x0" {
					return nil, fmt.Errorf("unexpected memory failure request")
				}
				expectedMessage = "M37 injected trace failure"
			default:
				return nil, fmt.Errorf("unexpected original provider error")
			}
			errorMethods[p.ChainID] = method
			errorTags[p.ChainID] = tag

			restored, e := registry.restore(p.Error)
			if e != nil {
				return nil, e
			}
			err, ok := restored.(error)
			if !ok || err.Error() != expectedMessage {
				return nil, fmt.Errorf("unexpected injected provider failure")
			}
			if providerErrors[p.ChainID] != nil {
				return nil, fmt.Errorf("duplicate provider error")
			}
			providerErrors[p.ChainID] = err
		}
		providers[p.ChainID] = append(providers[p.ChainID], historicalLiveProvider{Request: sourceMultichainWire(request[0]), Response: sourceMultichainWire(p.Response)})
	}
	if (call.Name == "latest-first-error" || call.Name == "memory-block-trace-first-error") && len(providerErrors) != 1 {
		return nil, fmt.Errorf("exactly one captured latest failure required")
	}
	perChain, e := historicalLiveObjects(s.args["perChainSync"])
	if e != nil {
		return nil, e
	}
	byID := map[uint64]SourceHistoricalChain{}
	for _, entry := range perChain {
		chain, e := historicalLiveObject(entry["chain"])
		if e != nil {
			return nil, e
		}
		id, e := historicalLiveUint(chain["id"])
		if e != nil {
			return nil, e
		}
		callbacks, ok := callbackByChain[id]
		if !ok {
			return nil, fmt.Errorf("perChainSync build mismatch")
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf("duplicate perChainSync chain")
		}
		p, e := historicalLiveObject(entry["syncProgress"])
		if e != nil {
			return nil, e
		}
		children, e := historicalLiveChildren(entry["childAddresses"])
		if e != nil {
			return nil, e
		}
		cached, e := sourceIndexPureCache(entry["cachedIntervals"])
		if e != nil {
			return nil, e
		}
		replay := &historicalLiveReplay{registry: registry}
		replay.load(providers[id])
		s.replays[id] = replay
		local := SourceLocalHistoricalOptions{ChainID: id, Requester: &sourceMultichainRequester{mu: &s.mu, replay: replay, providerError: providerErrors[id], errorMethod: errorMethods[id], errorTag: errorTags[id]}, EventCallbacks: callbacks, Children: NewSourceHistoricalChildren(children), Progress: &SourceSyncProgress{Start: p["start"].(filter.Object), End: sourceLocalOptionalObject(p["end"]), Current: sourceLocalOptionalObject(p["current"]), Finalized: p["finalized"].(filter.Object), ChainID: id}, CachedIntervals: cached, Store: source, Acquisition: SourceHistoricalOptions{FactoryAddressCountThreshold: int(threshold)}}
		if options["command"] == "dev" {
			local.TargetDuration = 2 * time.Second
		}
		if value, ok := chain["ethGetLogsBlockRange"]; ok && !filter.IsUndefined(value) && value != nil {
			n, e := historicalLiveUint(value)
			if e != nil {
				return nil, e
			}
			local.Acquisition.EthGetLogsBlockRange = &n
		}
		reorg, e := historicalLiveUint(chain["reorgWindow"])
		if e != nil {
			return nil, e
		}
		c := SourceHistoricalChain{Chain: chain, Local: local, CacheRPCRequests: chain["cacheRpcRequests"] == true, ReorgWindow: int(reorg)}
		if value, ok := chain["blockTime"]; ok && !filter.IsUndefined(value) && value != nil {
			n, e := historicalLiveNumber(value)
			if e != nil {
				return nil, e
			}
			c.BlockTimeMS = &n
		}
		s.options.Chains = append(s.options.Chains, c)
		byID[id] = c
	}
	for i, id := range buildIDs {
		c, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("missing perChainSync entry")
		}
		c.Chain = buildChains[i]
		s.options.BuildChains = append(s.options.BuildChains, c)
	}
	if value := s.args["crashRecoveryCheckpoint"]; value != nil && !filter.IsUndefined(value) {
		entries, e := historicalLiveObjects(value)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			id, e := historicalLiveUint(entry["chainId"])
			if e != nil {
				return nil, e
			}
			cp, ok := entry["checkpoint"].(string)
			if !ok {
				return nil, fmt.Errorf("crash checkpoint required")
			}
			s.options.CrashRecovery = append(s.options.CrashRecovery, SourceCrashRecoveryCheckpoint{ChainID: id, Checkpoint: cp})
		}
	}
	s.options.Clock = func() time.Time {
		s.clockMu.Lock()
		defer s.clockMu.Unlock()
		if s.clockIndex >= len(s.clocks) {
			s.clockError = fmt.Errorf("native coordinator made an uncaptured clock request")
			return time.UnixMilli(0)
		}
		n, e := s.clocks[s.clockIndex].Int64()
		s.clockIndex++
		if e != nil {
			s.clockError = e
		}
		return time.UnixMilli(n)
	}
	return s, nil
}
func (s *sourceMultichainReplayState) after() filter.Object {
	out := filter.Object{}
	for k, v := range s.args {
		out[k] = v
	}
	options := filter.Object{}
	for k, v := range s.args["options"].(filter.Object) {
		options[k] = v
	}
	out["options"] = options
	perChain := []any{}
	for i, c := range s.options.Chains {
		p := c.Local.Progress
		perChain = append(perChain, filter.Object{"chain": c.Chain, "syncProgress": filter.Object{"start": p.Start, "end": sourceIndexOptional(p.End), "current": sourceIndexOptional(p.Current), "finalized": p.Finalized}, "childAddresses": historicalLiveChildOutput(c.Local.Children), "cachedIntervals": s.args["perChainSync"].([]any)[i].(filter.Object)["cachedIntervals"]})
	}
	out["perChainSync"] = perChain
	return out
}
func sourceMultichainGroup(g SourceEventGroup) filter.Object {
	return filter.Object{"chainId": g.ChainID, "events": g.Events, "checkpoint": g.Checkpoint, "blockRange": []any{g.BlockRange[0], g.BlockRange[1]}}
}
func sourceMultichainReplay(ctx context.Context, registry *historicalLiveRegistry, call sourceMultichainCall, source *store.SourceSyncStore) error {
	s, e := newSourceMultichainReplay(registry, call, source)
	if e != nil {
		return e
	}
	next, stop := iter.Pull2(HistoricalSourceMultichain(ctx, s.options))
	defer stop()
	for i, step := range call.Steps {
		if step.Operation != "next" {
			return fmt.Errorf("unexpected original coordinator consumer operation")
		}
		s.mu.Lock()
		values, e := registry.restore(sourceMultichainWire(step.Args))
		s.mu.Unlock()
		if e != nil {
			return e
		}
		list, e := historicalLiveList(values)
		if e != nil || len(list) != 0 {
			return fmt.Errorf("original next arguments differ")
		}
		g, e, ok := next()
		if e != nil {
			return e
		}
		var value any = filter.Undefined
		if ok {
			value = sourceMultichainGroup(g)
		}
		s.mu.Lock()
		e = registry.compare(filter.Object{"done": !ok, "value": value}, sourceMultichainWire(call.Results[i]), fmt.Sprintf("coordinator.result[%d]", i))
		s.mu.Unlock()
		if e != nil {
			return e
		}
	}
	if _, e, ok := next(); e != nil || ok {
		return fmt.Errorf("unexpected extra native coordinator result: %v", e)
	}
	for _, c := range s.options.Chains {
		if e := c.Local.Requester.(*sourceMultichainRequester).finished(); e != nil {
			return e
		}
	}
	if s.clockError != nil {
		return s.clockError
	}
	if s.clockIndex != len(s.clocks) {
		return fmt.Errorf("native coordinator omitted source clock observation: %d/%d", s.clockIndex, len(s.clocks))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return registry.compare(s.after(), sourceMultichainWire(call.After), "coordinator.after")
}

func TestSourceRuntimeMultichainOriginal(t *testing.T) {
	var fixture struct {
		Cases        []sourceMultichainCase
		BackendCases map[string][]sourceMultichainCase `json:"backend_cases"`
	}
	raw, e := os.ReadFile("../testdata/runtime/source-runtime-multichain-live.json")
	if e != nil {
		t.Fatal(e)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if e = dec.Decode(&fixture); e != nil {
		t.Fatal(e)
	}
	var ledger struct{ Tests []sourceMultichainCase }
	raw, e = os.ReadFile("../parity/test-ledger.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &ledger); e != nil {
		t.Fatal(e)
	}
	if e = sourceMultichainValidate(fixture.Cases, ledger.Tests); e != nil {
		t.Fatal(e)
	}
	if len(fixture.BackendCases) != 2 {
		t.Fatal("both captured backends required")
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			cases := fixture.BackendCases[dialect]
			if e := sourceMultichainValidate(cases, ledger.Tests); e != nil {
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
					db, e := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{{Name: "multichain_probe", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}, fmt.Sprintf("ponder_go_multichain_%x", time.Now().UnixNano()), schema.Multichain)
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
						if e = sourceMultichainReplay(ctx, registry, call, source); e != nil {
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

func TestSourceRuntimeMultichainEvidenceCompleteness(t *testing.T) {
	var fixture struct{ Cases []sourceMultichainCase }
	var ledger struct{ Tests []sourceMultichainCase }
	raw, e := os.ReadFile("../testdata/runtime/source-runtime-multichain-live.json")
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
	if e = sourceMultichainValidate(fixture.Cases, ledger.Tests); e != nil {
		t.Fatal(e)
	}
	controls := map[string]func([]sourceMultichainCase){"assertion": func(c []sourceMultichainCase) { c[11].Assertions = nil }, "body": func(c []sourceMultichainCase) { c[11].TestSource += " " }, "operation": func(c []sourceMultichainCase) { c[11].Calls[0].Operation = "getHistoricalEventsOmnichain" }, "consumer": func(c []sourceMultichainCase) { c[11].Calls[0].Steps = nil }, "physical": func(c []sourceMultichainCase) { c[11].Calls[0].PhysicalAfter = nil }, "clock": func(c []sourceMultichainCase) { c[11].Calls[0].Clocks = nil }, "target": func(c []sourceMultichainCase) { c[0].Targeted = true }, "duplicate": func(c []sourceMultichainCase) { c[12] = c[11] }}
	for name, mutate := range controls {
		t.Run(name, func(t *testing.T) {
			var copy []sourceMultichainCase
			raw, _ := json.Marshal(fixture.Cases)
			json.Unmarshal(raw, &copy)
			mutate(copy)
			if sourceMultichainValidate(copy, ledger.Tests) == nil {
				t.Fatal("altered original evidence accepted")
			}
		})
	}
}

// Physical observations are comparison-only. Remove serialization identity
// wrappers, retaining scalar bigint tags used by the physical row comparator.
func sourceMultichainPhysical(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = sourceMultichainPhysical(v)
		}
		return out
	case map[string]any:
		if pairs, ok := x["$object"].([]any); ok {
			out := map[string]any{}
			for _, p := range pairs {
				a := p.([]any)
				out[a[0].(string)] = sourceMultichainPhysical(a[1])
			}
			return out
		}
		out := map[string]any{}
		for k, v := range x {
			out[k] = sourceMultichainPhysical(v)
		}
		return out
	default:
		return v
	}
}
func sourceMultichainProbeCall(entry map[string]any) (sourceMultichainCall, error) {
	raw, e := json.Marshal(entry)
	if e != nil {
		return sourceMultichainCall{}, e
	}
	var call sourceMultichainCall
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e = d.Decode(&call)
	return call, e
}
func sourceMultichainProbeReplay(ctx context.Context, entry map[string]any, source *store.SourceSyncStore, gate *sourceMultichainProbeGate) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	call, e := sourceMultichainProbeCall(entry)
	if e != nil {
		return e
	}
	name, _ := entry["name"].(string)
	if name == "latest-concurrent-reverse-completion" && entry["latestAdmission"] == nil {
		return fmt.Errorf("required source latest admission evidence missing")
	}
	if name == "cross-chain-crash-safe-cache" || name == "cross-chain-crash-uncached-start" {
		setup, ok := entry["setup_calls"].([]any)
		if !ok || len(setup) != 1 {
			return fmt.Errorf("exact real source cache priming call required")
		}
		queries, ok := entry["safeQueries"].([]any)
		if !ok {
			return fmt.Errorf("source safe recovery query evidence required")
		}
		expected := 0
		if name == "cross-chain-crash-safe-cache" {
			expected = 1
		}
		if len(queries) != expected {
			return fmt.Errorf("source safe recovery query count differs")
		}
	}
	registry := newSourceLiveRegistry()
	s, e := newSourceMultichainReplay(registry, call, source)
	if e != nil {
		return e
	}
	gatedStore := &sourceMultichainGatedStore{SourceSyncStore: source, gate: gate}
	for i := range s.options.Chains {
		s.options.Chains[i].Local.Store = gatedStore
	}
	for i := range s.options.BuildChains {
		s.options.BuildChains[i].Local.Store = gatedStore
	}
	for _, c := range s.options.Chains {
		r := c.Local.Requester.(*sourceMultichainRequester)
		r.chainID = c.Local.ChainID
		r.gate = gate
	}
	var latestTimeline []any
	latestAdmission, hasLatest := entry["latestAdmission"].(map[string]any)
	if hasLatest {
		if latestAdmission["responseGate"] == nil {
			return fmt.Errorf("latest response causal gate missing")
		}
		for _, c := range s.options.Chains {
			c.Local.Requester.(*sourceMultichainRequester).latestTimeline = &latestTimeline
		}
	}

	var sequence iter.Seq2[any, error]
	switch entry["mode"] {
	case "multichain":
		sequence = func(yield func(any, error) bool) {
			for g, e := range HistoricalSourceMultichain(ctx, s.options) {
				if !yield(sourceMultichainGroup(g), e) {
					return
				}
			}
		}
	case "isolated":
		sequence = func(yield func(any, error) bool) {
			for g, e := range HistoricalSourceIsolated(ctx, s.options, s.options.Chains[0].Local.ChainID) {
				if !yield(sourceMultichainGroup(g), e) {
					return
				}
			}
		}
	case "omnichain":
		sequence = func(yield func(any, error) bool) {
			for o, e := range HistoricalSourceOmnichain(ctx, s.options) {
				var result any = o.Pending
				if o.Type == "events" {
					groups := []any{}
					for _, g := range o.Groups {
						groups = append(groups, sourceMultichainGroup(g))
					}
					result = groups
				}
				if !yield(filter.Object{"type": o.Type, "result": result}, e) {
					return
				}
			}
		}
	default:
		return fmt.Errorf("unknown source probe mode %v", entry["mode"])
	}
	next, stop := iter.Pull2(sequence)
	defer func() { cancel(); stop() }()
	resultIndex := 0
	ended := false
	gotError := false
	for _, step := range call.Steps {
		if step.Operation == "return" {
			stop()
			ended = true
			if e = registry.compare(filter.Object{"done": true, "value": filter.Undefined}, sourceMultichainWire(entry["return_result"]), "probe.return"); e != nil {
				return e
			}
			continue
		}
		if step.Operation != "next" {
			return fmt.Errorf("unknown source consumer operation %q", step.Operation)
		}
		value, err, ok := next()
		if err != nil {
			if entry["error"] == nil {
				return err
			}
			if e = registry.compare(err, entry["error"], "probe.error"); e != nil {
				return e
			}
			if gate.errorHeld != 0 {
				gate.mu.Lock()
				if gate.errorReleased {
					gate.mu.Unlock()
					return fmt.Errorf("held response released before first error")
				}
				gate.events = append(gate.events, map[string]any{"kind": "iterator_error"}, map[string]any{"kind": "cleanup_release"})
				gate.errorReleased = true
				gate.signal()
				gate.mu.Unlock()
			}
			gotError = true
			ended = true
			continue
		}

		if resultIndex >= len(call.Results) {
			return fmt.Errorf("uncaptured native result")
		}
		if !ok {
			value = filter.Undefined
			ended = true
		}
		s.mu.Lock()
		e = registry.compare(filter.Object{"done": !ok, "value": value}, sourceMultichainWire(call.Results[resultIndex]), fmt.Sprintf("probe.result[%d]", resultIndex))
		s.mu.Unlock()
		if e != nil {
			return e
		}
		if ok {
			if group, yes := value.(filter.Object); yes && group["chainId"] != nil {
				gate.yielded(group)
			}
		}
		resultIndex++
	}
	stop()
	gate.workers.Wait()
	if !ended || resultIndex != len(call.Results) {
		return fmt.Errorf("incomplete source consumer replay")
	}
	if (entry["error"] != nil) != gotError {
		return fmt.Errorf("missing source error")
	}
	for _, c := range s.options.Chains {
		if e = c.Local.Requester.(*sourceMultichainRequester).finished(); e != nil {
			return e
		}
	}
	if s.clockError != nil {
		return s.clockError
	}
	if s.clockIndex != len(s.clocks) {
		return fmt.Errorf("clock count native%d source%d", s.clockIndex, len(s.clocks))
	}
	if e = registry.compare(s.after(), sourceMultichainWire(call.After), "probe.after"); e != nil {
		return e
	}
	if hasLatest {
		a, _ := json.Marshal(latestTimeline)
		b, _ := json.Marshal(latestAdmission["timeline"])
		if !bytes.Equal(a, b) {
			return fmt.Errorf("latest admission/completion trajectory native%s source%s", a, b)
		}
	}

	if e = gate.verifyEvents(entry["control_events"]); e != nil {
		return e
	}
	gate.enabled = false
	return sourceHistoryVerifyProbeHelpers(ctx, registry, s, entry, source)
}

var sourceMultichainProbeNames = map[string]bool{
	"two-chain-completion-mixed-cache": true, "two-chain-pending": true, "two-chain-pending-catchup": true, "two-chain-multichain-catchup": true,
	"real-finalized-backward-multichain": true, "real-finalized-backward-omnichain": true, "real-finalized-backward-isolated": true, "two-chain-refetch": true,
	"isolated-memory-stop": true, "refetch-all-filters-extras": true, "refetch-sparse-slots": true, "aggregate-checkpoint-tags": true,
	"cross-chain-crash-safe-cache": true, "cross-chain-crash-uncached-start": true, "bounded-end-empty-chain": true, "latest-concurrent-reverse-completion": true,
}

// Observe actual SQL through both direct and transaction paths, while all reads
// still execute against the real backend. No expected rows enter production.
type sourceMultichainObservedBackend struct {
	store.Backend
	mu   sync.Mutex
	safe []filter.Object
}
type sourceMultichainObservedTx struct {
	store.Transaction
	owner *sourceMultichainObservedBackend
}

func (b *sourceMultichainObservedBackend) Begin(ctx context.Context) (store.Transaction, error) {
	tx, e := b.Backend.Begin(ctx)
	if e != nil {
		return nil, e
	}
	return &sourceMultichainObservedTx{Transaction: tx, owner: b}, nil
}
func (b *sourceMultichainObservedBackend) observe(sql string, args []any, result store.Result, err error) {
	if !strings.Contains(sql, "WHERE chain_id=$1 AND timestamp<$2 ORDER BY number DESC LIMIT 1") {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var value any = nil
	if err == nil && len(result.Rows) > 0 {
		row := result.Rows[0]
		n, _ := new(big.Int).SetString(fmt.Sprint(row["number"]), 10)
		ts, _ := new(big.Int).SetString(fmt.Sprint(row["timestamp"]), 10)
		value = filter.Object{"number": n, "timestamp": ts}
	}
	b.safe = append(b.safe, filter.Object{"args": filter.Object{"chainId": args[0], "timestamp": args[1]}, "result": value})
}
func (b *sourceMultichainObservedBackend) Query(ctx context.Context, q string, args ...any) (store.Result, error) {
	r, e := b.Backend.Query(ctx, q, args...)
	b.observe(q, args, r, e)
	return r, e
}
func (t *sourceMultichainObservedTx) Query(ctx context.Context, q string, args ...any) (store.Result, error) {
	r, e := t.Transaction.Query(ctx, q, args...)
	t.owner.observe(q, args, r, e)
	return r, e
}

// Gate the production event-data response only after the real store call and
// all SQL/Store locks have returned. This wrapper does not replace query logic.
type sourceMultichainGatedStore struct {
	*store.SourceSyncStore
	gate *sourceMultichainProbeGate
}

func (s *sourceMultichainGatedStore) GetEventData(ctx context.Context, args store.SourceEventDataArgs) (store.SourceEventData, error) {
	data, e := s.SourceSyncStore.GetEventData(ctx, args)
	if e != nil {
		return data, e
	}
	g := s.gate
	if g != nil && g.enabled && args.ChainID == g.queryChain {
		g.event("query_response_held", args.ChainID, "toBlock", args.ToBlock)
		if e = g.wait(ctx, func() bool { return g.through(g.queryAfter, args.ToBlock) }); e != nil {
			return store.SourceEventData{}, e
		}
		g.event("query_response_released", args.ChainID, "toBlock", args.ToBlock)
	}
	return data, nil
}

type sourceMultichainProbeGate struct {
	mu                                                                                     sync.Mutex
	wake                                                                                   chan struct{}
	yields                                                                                 map[uint64]float64
	responses                                                                              map[uint64]bool
	events                                                                                 []any
	enabled                                                                                bool
	errorHeld, errorChain                                                                  uint64
	errorReleased                                                                          bool
	memory                                                                                 bool
	workers                                                                                sync.WaitGroup
	queryChain, queryAfter, firstChain, firstAfter, firstThrough, latestChain, latestAfter uint64
}

func newSourceMultichainProbeGate(entry map[string]any) (*sourceMultichainProbeGate, error) {
	g := &sourceMultichainProbeGate{wake: make(chan struct{}), enabled: true, yields: map[uint64]float64{}, responses: map[uint64]bool{}}
	read := func(m map[string]any, k string) (uint64, error) { n, e := historicalLiveUint(m[k]); return n, e }
	if controls, ok := entry["controls"].(map[string]any); ok {
		for key, value := range controls {
			m, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid causal control")
			}
			var e error
			switch key {
			case "memoryFirstError":
				g.errorHeld, e = read(m, "chainId")
				if e != nil {
					return nil, e
				}
				n, e := read(m, "blockNumber")
				if e != nil || n != 0 || g.errorHeld != 1 || m["message"] != "M37 injected trace failure" {
					return nil, fmt.Errorf("unexpected source memoryerror controls")
				}
				g.memory = true
				g.errorChain = 1
			case "latestFirstError":
				g.errorHeld, e = read(m, "heldChainId")
				if e != nil {
					return nil, e
				}
				g.errorChain, e = read(m, "errorChainId")
				if g.errorHeld != 1 || g.errorChain != 2 || m["message"] != "M37 injected latest failure" {
					return nil, fmt.Errorf("unexpected source firsterror controls")
				}
			case "eventQueryGate":
				g.queryChain, e = read(m, "chainId")
				if e != nil {
					return nil, e
				}
				g.queryAfter, e = read(m, "afterYieldChainId")
				if m["throughQueryToBlock"] != true {
					return nil, fmt.Errorf("querygate must use actual querybound")
				}
			case "firstBlockResponseGate":
				g.firstChain, e = read(m, "chainId")
				if e != nil {
					return nil, e
				}
				g.firstAfter, e = read(m, "afterYieldChainId")
				if e != nil {
					return nil, e
				}
				g.firstThrough, e = read(m, "throughBlock")
			default:
				return nil, fmt.Errorf("unknown input control %s", key)
			}
			if e != nil {
				return nil, e
			}
		}
	}
	if latest, ok := entry["latestAdmission"].(map[string]any); ok {
		m, ok := latest["responseGate"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("latest causal response gate required")
		}
		var e error
		g.latestChain, e = read(m, "chainId")
		if e != nil {
			return nil, e
		}
		g.latestAfter, e = read(m, "afterResponseChainId")
		if e != nil {
			return nil, e
		}
	}
	return g, nil
}
func (g *sourceMultichainProbeGate) through(id uint64, n float64) bool {
	v, ok := g.yields[id]
	return ok && v >= n
}
func (g *sourceMultichainProbeGate) wait(ctx context.Context, ready func() bool) error {
	for {
		g.mu.Lock()
		if ready() {
			g.mu.Unlock()
			return nil
		}
		wake := g.wake
		g.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (g *sourceMultichainProbeGate) signal() { close(g.wake); g.wake = make(chan struct{}) }
func (g *sourceMultichainProbeGate) yielded(group filter.Object) {
	id, e := historicalLiveUint(group["chainId"])
	if e != nil {
		return
	}
	span, e := historicalLiveList(group["blockRange"])
	if e != nil || len(span) != 2 {
		return
	}
	end, e := historicalLiveNumber(span[1])
	if e != nil {
		return
	}
	g.mu.Lock()
	g.events = append(g.events, map[string]any{"kind": "consumer_yield", "chainId": id, "throughBlock": end})
	g.yields[id] = end
	g.signal()
	g.mu.Unlock()
}
func (g *sourceMultichainProbeGate) memoryEvent(kind string) {
	g.mu.Lock()
	g.events = append(g.events, map[string]any{"kind": kind, "chainId": uint64(1), "blockNumber": float64(0)})
	g.mu.Unlock()
}
func (g *sourceMultichainProbeGate) special(kind string, id uint64) {
	g.mu.Lock()
	m := map[string]any{"kind": kind}
	if id != 0 {
		m["chainId"] = id
	}
	g.events = append(g.events, m)
	g.mu.Unlock()
}
func (g *sourceMultichainProbeGate) event(kind string, id uint64, key string, n float64) {
	g.mu.Lock()
	g.events = append(g.events, map[string]any{"kind": kind, "chainId": id, key: n})
	g.mu.Unlock()
}

// SQL readiness is not ordered across independent stores. Compare the exact
// per-kind observations and independently require every response release to be
// causally after its specified actual consumer yield (never sort output groups).
func (g *sourceMultichainProbeGate) verifyEvents(expected any) error {
	want, ok := expected.([]any)
	if !ok {
		return fmt.Errorf("source control events required")
	}
	project := func(events []any) (map[string][]any, error) {
		out := map[string][]any{}
		yields := map[uint64]float64{}
		held := map[string]int{}
		for _, v := range events {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid control observation")
			}
			kind, _ := m["kind"].(string)
			if strings.HasPrefix(kind, "latest_") || kind == "iterator_error" || kind == "cleanup_release" || (g.memory && (kind == "block_begin" || kind == "trace_begin" || kind == "trace_error" || kind == "block_released" || kind == "block_cancelled")) {
				continue
			}
			id, e := historicalLiveUint(m["chainId"])
			if e != nil {
				return nil, e
			}
			out[kind] = append(out[kind], m)
			if kind == "consumer_yield" {
				n, e := historicalLiveNumber(m["throughBlock"])
				if e != nil {
					return nil, e
				}
				yields[id] = n
				continue
			}
			key := "toBlock"
			after := g.queryAfter
			if strings.HasPrefix(kind, "block_") {
				key = "throughBlock"
				after = g.firstAfter
			}
			n, e := historicalLiveNumber(m[key])
			if e != nil {
				return nil, e
			}
			token := fmt.Sprintf("%d/%g/%s", id, n, key)
			if strings.HasSuffix(kind, "_held") {
				held[token]++
			} else if strings.HasSuffix(kind, "_released") {
				v, ok := yields[after]
				if !ok || v < n || held[token] == 0 {
					return nil, fmt.Errorf("response released before causal yield: %v", m)
				}
				held[token]--
			} else {
				return nil, fmt.Errorf("unknown control event")
			}
		}
		for _, n := range held {
			if n != 0 {
				return nil, fmt.Errorf("unreleased control response")
			}
		}
		return out, nil
	}
	if g.errorHeld != 0 {
		validate := func(events []any, native bool) error {
			begins := []uint64{}
			errorIndex, iteratorIndex, cleanupIndex, terminalIndex := -1, -1, -1, -1
			for i, v := range events {
				m := v.(map[string]any)
				switch m["kind"] {
				case "latest_begin", "block_begin", "trace_begin":
					id, e := historicalLiveUint(m["chainId"])
					if e != nil {
						return e
					}
					begins = append(begins, id)
				case "latest_error", "trace_error":
					if fmt.Sprint(m["chainId"]) != fmt.Sprint(g.errorChain) {
						return fmt.Errorf("wrong source errorchain")
					}
					errorIndex = i
				case "iterator_error":
					iteratorIndex = i
				case "cleanup_release":
					cleanupIndex = i
				case "latest_released", "latest_cancelled", "block_released", "block_cancelled":
					terminalIndex = i
				}
			}
			expectedBegins := []uint64{1, 2}
			if g.memory {
				expectedBegins = []uint64{1, 1}
			}
			if !reflect.DeepEqual(begins, expectedBegins) || errorIndex < 0 || iteratorIndex <= errorIndex || cleanupIndex <= iteratorIndex || terminalIndex < 0 {
				return fmt.Errorf("firsterror causal trace incomplete: %v", events)
			}
			if !native && terminalIndex <= cleanupIndex {
				return fmt.Errorf("source pending response released before cleanup")
			}
			return nil
		}
		if e := validate(want, false); e != nil {
			return e
		}
		if e := validate(g.events, true); e != nil {
			return e
		}
	}
	a, e := project(g.events)
	if e != nil {
		return e
	}
	b, e := project(want)
	if e != nil {
		return e
	}
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	if !bytes.Equal(left, right) {
		return fmt.Errorf("control observations differ native%s source%s", left, right)
	}
	return nil
}
func (g *sourceMultichainProbeGate) response(id uint64) {
	g.mu.Lock()
	g.responses[id] = true
	g.signal()
	g.mu.Unlock()
}
func TestSourceRuntimeMultichainProbes(t *testing.T) {
	raw, e := os.ReadFile("../testdata/runtime/source-runtime-multichain-probes.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		BackendCases map[string][]map[string]any `json:"backend_cases"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e = d.Decode(&fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.BackendCases) != 2 {
		t.Fatal("both source backends required")
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			cases := fixture.BackendCases[dialect]
			if len(cases) != 16 {
				t.Fatal("complete supplemental capture required")
			}
			seen := map[string]bool{}
			for _, entry := range cases {
				name, ok := entry["name"].(string)
				if !ok || seen[name] || !sourceMultichainProbeNames[name] {
					t.Fatal("missing/duplicate probe name")
				}
				seen[name] = true
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					if dialect == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
						t.Fatal("explicit PostgreSQL required")
					}
					if fmt.Sprint(entry["source_credit"]) != "0" || entry["real_database"] != true {
						t.Fatal("supplemental real-source boundary missing")
					}
					backend := &sourceMultichainObservedBackend{Backend: stagedBackend(t, ctx, dialect, "memory://")}
					defer backend.Close()
					db, e := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{{Name: "multichain_probe", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}, fmt.Sprintf("ponder_go_multichain_supp_%x", time.Now().UnixNano()), schema.Multichain)
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
					calls := []map[string]any{}
					if setup, ok := entry["setup_calls"].([]any); ok {
						for _, v := range setup {
							calls = append(calls, v.(map[string]any))
						}
					}
					calls = append(calls, entry)
					if nested, ok := entry["error_call"].(map[string]any); ok {
						if name != "latest-concurrent-reverse-completion" || nested["name"] != "latest-first-error" {
							t.Fatal("unexpected nested error call")
						}
						calls = append(calls, nested)
					} else if name == "latest-concurrent-reverse-completion" {
						t.Fatal("required nested firsterror call missing")
					}
					if nested, ok := entry["memory_error_call"].(map[string]any); ok {
						if name != "isolated-memory-stop" || nested["name"] != "memory-block-trace-first-error" {
							t.Fatal("unexpected nested memoryerror call")
						}
						calls = append(calls, nested)
					} else if name == "isolated-memory-stop" {
						t.Fatal("required memory firsterror call missing")
					}
					for i, call := range calls {
						backend.mu.Lock()
						backend.safe = nil
						backend.mu.Unlock()
						before, ok := sourceMultichainPhysical(call["physical_before"]).(map[string]any)
						if !ok {
							t.Fatal("physical before missing")
						}
						if e = historicalLiveComparePhysical(ctx, backend, cache, before); e != nil {
							t.Fatalf("call%d before: %v", i, e)
						}
						gate, e := newSourceMultichainProbeGate(call)
						if e != nil {
							t.Fatal(e)
						}
						if e = sourceMultichainProbeReplay(ctx, call, source, gate); e != nil {
							t.Fatalf("call%d: %v", i, e)
						}
						events, _ := json.Marshal(gate.events)
						t.Logf("call%d actual control events: %s", i, events)
						if expected, exists := call["safeQueries"]; exists {
							backend.mu.Lock()
							observed := append([]filter.Object{}, backend.safe...)
							backend.mu.Unlock()
							want, ok := expected.([]any)
							if !ok || len(want) != len(observed) {
								t.Fatalf("safe recovery query count native%d source%v", len(observed), expected)
							}
							registry := newSourceLiveRegistry()
							for n, raw := range want {
								entry, ok := raw.(map[string]any)
								if !ok || len(entry) != 2 {
									t.Fatal("safe query args/result evidence required")
								}
								for _, key := range []string{"args", "result"} {
									if e = registry.compare(observed[n][key], entry[key], fmt.Sprintf("safeRecoveryQueries[%d].%s", n, key)); e != nil {
										t.Fatal(e)
									}
								}
							}
						}

						after, ok := sourceMultichainPhysical(call["physical_after"]).(map[string]any)
						if !ok {
							t.Fatal("physical after missing")
						}
						if e = historicalLiveComparePhysical(ctx, backend, cache, after); e != nil {
							t.Fatalf("call%d after: %v", i, e)
						}
					}
				})
			}
		})
	}
}

func TestSourceRuntimeMultichainProviderErrorAdmission(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async_%v", async), func(t *testing.T) {
			sentinel := errors.New("captured injected provider error")
			makeRequester := func() *sourceMultichainRequester {
				replay := &historicalLiveReplay{registry: newSourceLiveRegistry()}
				replay.load([]historicalLiveProvider{{Request: map[string]any{"$id": 1, "$object": []any{[]any{"method", "eth_getBlockByNumber"}, []any{"params", []any{"latest", true}}}}, Response: nil}})
				return &sourceMultichainRequester{mu: &sync.Mutex{}, replay: replay, providerError: sentinel, errorMethod: "eth_getBlockByNumber", errorTag: "latest"}
			}
			call := func(ctx context.Context, r *sourceMultichainRequester, full bool) error {
				var out filter.Object
				if async {
					return <-r.BeginCall(ctx, "eth_getBlockByNumber", []any{"latest", full}, &out)
				}
				return r.Call(ctx, "eth_getBlockByNumber", []any{"latest", full}, &out)
			}
			t.Run("legitimate", func(t *testing.T) {
				r := makeRequester()
				if e := call(context.Background(), r, true); e != sentinel {
					t.Fatalf("captured injection lost: %v", e)
				}
				if e := r.finished(); e != nil {
					t.Fatal(e)
				}
			})
			t.Run("duplicate_is_sticky", func(t *testing.T) {
				r := makeRequester()
				_ = call(context.Background(), r, true)
				e := call(context.Background(), r, true)
				if e == nil || e == sentinel {
					t.Fatal("duplicate request masked by injection")
				}
				if r.finished() == nil {
					t.Fatal("ignored duplicate error escaped final completeness")
				}
			})
			t.Run("mismatch_is_sticky", func(t *testing.T) {
				r := makeRequester()
				e := call(context.Background(), r, false)
				if e == nil || e == sentinel {
					t.Fatal("mismatched request masked by injection")
				}
				_ = call(context.Background(), r, true)
				if r.finished() == nil {
					t.Fatal("ignored mismatch escaped final completeness")
				}
			})
			t.Run("cancellation_not_injection", func(t *testing.T) {
				r := makeRequester()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if e := call(ctx, r, true); !errors.Is(e, context.Canceled) {
					t.Fatalf("cancellation masked: %v", e)
				}
			})
		})
	}
}
