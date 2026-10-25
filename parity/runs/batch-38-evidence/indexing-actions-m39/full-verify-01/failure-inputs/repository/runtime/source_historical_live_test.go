package runtime

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

	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type historicalLiveProvider struct {
	Request, Response any
	Error             *struct {
		Name, Message string
		Code          int
	}
}
type historicalLiveReplay struct {
	mu        sync.Mutex
	registry  *historicalLiveRegistry
	providers []historicalLiveProvider
	used      []bool
	requests  []string
}

func historicalLiveRequestKey(method string, params any) (string, error) {
	raw, err := json.Marshal([]any{method, filter.RPCValue(params)})
	return string(raw), err
}
func (r *historicalLiveReplay) load(providers []historicalLiveProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = providers
	r.used = make([]bool, len(providers))
	r.requests = []string{}
}
func (r *historicalLiveReplay) Call(ctx context.Context, method string, params, out any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := historicalLiveRequestKey(method, params)
	if err != nil {
		return err
	}
	r.requests = append(r.requests, key)
	for i, provider := range r.providers {
		if r.used[i] {
			continue
		}
		value, err := r.registry.restore(provider.Request)
		if err != nil {
			return err
		}
		request, err := historicalLiveObject(value)
		if err != nil {
			return err
		}
		wanted, err := historicalLiveRequestKey(request["method"].(string), request["params"])
		if err != nil {
			return err
		}
		if wanted != key {
			continue
		}
		r.used[i] = true
		if provider.Error != nil {
			return &rpc.Error{Code: provider.Error.Code, Message: provider.Error.Message}
		}
		value, err = r.registry.restore(provider.Response)
		if err != nil {
			return err
		}
		target := reflect.ValueOf(out)
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return fmt.Errorf("provider target must be pointer")
		}
		target = target.Elem()
		if value == nil {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(target.Type()) {
			target.Set(source)
			return nil
		}
		if target.Kind() == reflect.Slice && source.Kind() == reflect.Slice {
			result := reflect.MakeSlice(target.Type(), source.Len(), source.Len())
			for i := 0; i < source.Len(); i++ {
				entry := reflect.ValueOf(source.Index(i).Interface())
				if !entry.Type().AssignableTo(target.Type().Elem()) {
					return fmt.Errorf("provider slice target mismatch %v", target.Type())
				}
				result.Index(i).Set(entry)
			}
			target.Set(result)
			return nil
		}
		return fmt.Errorf("provider target mismatch %T to %v", value, target.Type())
	}
	return fmt.Errorf("native historical made an uncaptured provider request: %s", key)
}
func (r *historicalLiveReplay) finished() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, used := range r.used {
		if !used {
			return fmt.Errorf("native historical omitted source logical provider request %d", i)
		}
	}
	return nil
}

type historicalLiveInstance struct {
	memory     *SourceInMemoryHistoricalSync
	persistent *SourceHistoricalSync
	rpc        *historicalLiveReplay
	children   *SourceHistoricalChildren
}
type historicalLiveRecord struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	FullName       string `json:"full_name"`
	File           string `json:"source_file"`
	SourceSHA      string `json:"source_sha256"`
	Body           string `json:"test_source"`
	Assertions     []any  `json:"assertions"`
	TypeDirectives []any  `json:"type_error_directives"`
}
type historicalLiveCase struct {
	record        historicalLiveRecord
	registry      *historicalLiveRegistry
	instances     map[uint64]*historicalLiveInstance
	rpcs          map[uint64]*historicalLiveReplay
	actions       map[uint64]*rpc.Actions
	children      map[uint64]*SourceHistoricalChildren
	database      *store.SourceSyncStore
	cache         *store.ProtocolCache
	backend       store.Backend
	dialect       string
	backendClosed bool
	calls         []map[string]any
	errors        []string
	complete      bool
	typesStarted  bool
	typesDone     bool
	typeRuns      []map[string]any
}
type historicalLiveRequest struct {
	CaseID         string `json:"case_id"`
	Sequence       int    `json:"sequence"`
	Operation      string `json:"operation"`
	Args, Result   any
	Providers      []historicalLiveProvider
	Children       any
	Physical       map[string]any  `json:"physical_rows"`
	SourceID       string          `json:"source_id"`
	DirectiveIndex int             `json:"directive_index"`
	Payload        json.RawMessage `json:"payload"`
}

func historicalLiveReadJSON(file string, out any) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}
func historicalLiveSpan(value any) (store.SourceInterval, error) {
	list, err := historicalLiveList(value)
	if err != nil || len(list) != 2 {
		return store.SourceInterval{}, fmt.Errorf("historical interval tuple required")
	}
	from, err := historicalLiveNumber(list[0])
	if err != nil {
		return store.SourceInterval{}, err
	}
	to, err := historicalLiveNumber(list[1])
	return store.SourceInterval{from, to}, err
}
func historicalLiveIntervals(args filter.Object) ([]store.SourceFilterInterval, []store.SourceFactoryInterval, error) {
	required := []store.SourceFilterInterval{}
	factories := []store.SourceFactoryInterval{}
	if value, ok := args["requiredIntervals"]; ok {
		list, err := historicalLiveObjects(value)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range list {
			f, err := historicalLiveObject(item["filter"])
			if err != nil {
				return nil, nil, err
			}
			span, err := historicalLiveSpan(item["interval"])
			if err != nil {
				return nil, nil, err
			}
			required = append(required, store.SourceFilterInterval{Filter: f, Interval: span})
		}
	}
	if value, ok := args["requiredFactoryIntervals"]; ok {
		list, err := historicalLiveObjects(value)
		if err != nil {
			return nil, nil, err
		}
		for _, item := range list {
			f, err := historicalLiveObject(item["factory"])
			if err != nil {
				return nil, nil, err
			}
			span, err := historicalLiveSpan(item["interval"])
			if err != nil {
				return nil, nil, err
			}
			factories = append(factories, store.SourceFactoryInterval{Factory: f, Interval: span})
		}
	}
	return required, factories, nil
}
func historicalLiveChildren(value any) ([]SourceFactoryChildren, error) {
	pairs, ok := value.(historicalLiveMap)
	if !ok {
		return nil, fmt.Errorf("historical child map required")
	}
	result := []SourceFactoryChildren{}
	for _, pair := range pairs {
		id, ok := pair.Key.(string)
		if !ok {
			return nil, fmt.Errorf("factory map key must be string")
		}
		entries, ok := pair.Value.(historicalLiveMap)
		if !ok {
			return nil, fmt.Errorf("factory addresses must be Map")
		}
		item := SourceFactoryChildren{ID: id, Entries: []filter.ChildAddress{}}
		for _, entry := range entries {
			address, ok := entry.Key.(string)
			if !ok {
				return nil, fmt.Errorf("child address must be string")
			}
			number, err := historicalLiveUint(entry.Value)
			if err != nil {
				return nil, err
			}
			item.Entries = append(item.Entries, filter.ChildAddress{Address: address, Block: number})
		}
		result = append(result, item)
	}
	return result, nil
}
func historicalLiveChildOutput(children *SourceHistoricalChildren) historicalLiveMap {
	result := historicalLiveMap{}
	for _, item := range children.Snapshot() {
		entries := historicalLiveMap{}
		for _, entry := range item.Entries {
			entries = append(entries, historicalLivePair{Key: entry.Address, Value: float64(entry.Block)})
		}
		result = append(result, historicalLivePair{Key: item.ID, Value: entries})
	}
	return result
}

func (c *historicalLiveCase) construct(ctx context.Context, kind string, args filter.Object) error {
	id, err := historicalLiveUint(args["instance"])
	if err != nil {
		return err
	}
	rpcID, err := historicalLiveUint(args["rpcID"])
	if err != nil {
		return err
	}
	childID, err := historicalLiveUint(args["childID"])
	if err != nil {
		return err
	}
	if _, exists := c.instances[id]; exists {
		return fmt.Errorf("duplicate historical constructor")
	}
	requester, ok := c.rpcs[rpcID]
	if !ok {
		requester = &historicalLiveReplay{registry: c.registry}
		c.rpcs[rpcID] = requester
		c.actions[rpcID] = rpc.NewActions(requester)
	}
	children, ok := c.children[childID]
	if !ok {
		entries, err := historicalLiveChildren(args["children"])
		if err != nil {
			return err
		}
		children = NewSourceHistoricalChildren(entries)
		c.children[childID] = children
	}
	chain, err := historicalLiveObject(args["chain"])
	if err != nil {
		return err
	}
	chainID, err := historicalLiveUint(chain["id"])
	if err != nil {
		return err
	}
	rawOptions, err := historicalLiveObject(args["options"])
	if err != nil {
		return err
	}
	threshold, err := historicalLiveUint(rawOptions["factoryAddressCountThreshold"])
	if err != nil {
		return err
	}
	options := SourceHistoricalOptions{FactoryAddressCountThreshold: int(threshold)}
	if size := chain["ethGetLogsBlockRange"]; size != nil && !filter.IsUndefined(size) {
		number, err := historicalLiveUint(size)
		if err != nil {
			return err
		}
		options.EthGetLogsBlockRange = &number
	}
	instance := &historicalLiveInstance{rpc: requester, children: children}
	if kind == "memory" {
		instance.memory, err = NewSourceInMemoryHistoricalSync(requester, c.actions[rpcID], children, options)
	} else {
		instance.persistent, err = NewSourceHistoricalSync(chainID, requester, c.actions[rpcID], children, options)
	}
	if err != nil {
		return err
	}
	c.instances[id] = instance
	return nil
}

func (c *historicalLiveCase) closeBackend() error {
	if c.cache != nil {
		if err := c.cache.Close(); err != nil {
			return err
		}
	}
	if c.backend != nil {
		if err := c.backend.Close(); err != nil {
			return err
		}
		c.backend = nil
	}
	c.backendClosed = true
	return nil
}
func (c *historicalLiveCase) openBackend(ctx context.Context) error {
	if c.backend != nil {
		return nil
	}
	var err error
	if c.dialect == "postgres" {
		address := os.Getenv("PONDER_TEST_POSTGRES")
		parsed, parseErr := url.Parse(address)
		if parseErr != nil || parsed.Hostname() != "127.0.0.1" || (parsed.Port() != "53398" && !(os.Getenv("PONDER_UPSTREAM_ROOT") != "" && parsed.Port() == "55432")) || parsed.Path != "/ponder_test" {
			return fmt.Errorf("dedicated task PostgreSQL required")
		}
		c.backend, err = store.OpenPostgres(ctx, address)
	} else {
		c.backend, err = store.OpenPGlite(ctx, "memory://")
	}
	if err == nil {
		c.backendClosed = false
	}
	return err
}
func (c *historicalLiveCase) invoke(ctx context.Context, call historicalLiveRequest) (any, error) {
	value, err := c.registry.restore(call.Args)
	if err != nil {
		return nil, err
	}
	args, err := historicalLiveObject(value)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(call.Operation, "construct/") {
		err = c.construct(ctx, strings.TrimPrefix(call.Operation, "construct/"), args)
		return true, err
	}
	if call.Operation == "store/create" {
		if c.database == nil {
			if err = c.openBackend(ctx); err != nil {
				return nil, err
			}
			db, err := store.New(ctx, c.backend, schema.Schema{Tables: []schema.Table{{Name: "audit", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}}}}}, fmt.Sprintf("ponder_go_historical_live_%x", time.Now().UnixNano()), schema.Multichain)
			if err != nil {
				return nil, err
			}
			c.cache, err = db.EnableProtocolCache(ctx)
			if err != nil {
				return nil, err
			}
			c.database, err = c.cache.SourceSyncStore(0)
			if err != nil {
				return nil, err
			}
		}
		return true, nil
	}
	if strings.HasPrefix(call.Operation, "store/") {
		if c.database == nil {
			return nil, fmt.Errorf("native source store missing")
		}
		switch call.Operation {
		case "store/insertIntervals":
			args["requiredIntervals"] = args["intervals"]
			args["requiredFactoryIntervals"] = args["factoryIntervals"]
			required, factories, err := historicalLiveIntervals(args)
			if err != nil {
				return nil, err
			}
			chain, err := historicalLiveUint(args["chainId"])
			if err != nil {
				return nil, err
			}
			return filter.Undefined, c.database.InsertIntervals(ctx, chain, required, factories)
		case "store/getIntervals":
			filters, err := historicalLiveObjects(args["filters"])
			if err != nil {
				return nil, err
			}
			return c.database.GetIntervals(ctx, filters)
		case "store/getChildAddresses":
			factory, err := historicalLiveObject(args["factory"])
			if err != nil {
				return nil, err
			}
			return c.database.GetChildAddresses(ctx, factory)
		}
		return nil, fmt.Errorf("unexpected external source store operation %s", call.Operation)
	}
	if call.Operation == "merge" {
		input, err := historicalLiveObjects(args["generators"])
		if err != nil {
			return nil, err
		}
		generators := []SourceIntervalGenerator{}
		calls := []any{}
		for index, item := range input {
			span, err := historicalLiveSpan(item["interval"])
			if err != nil {
				return nil, err
			}
			steps, err := historicalLiveObjects(item["steps"])
			if err != nil {
				return nil, err
			}
			position := 0
			generators = append(generators, SourceIntervalGenerator{Interval: span, Next: func(ctx context.Context) <-chan SourceIntervalStep {
				calls = append(calls, float64(index))
				future := make(chan SourceIntervalStep, 1)
				if position >= len(steps) {
					future <- SourceIntervalStep{Err: fmt.Errorf("native merge over-read generator")}
					close(future)
					return future
				}
				step := steps[position]
				position++
				done, _ := step["done"].(bool)
				result := SourceIntervalStep{Done: done}
				if !done {
					result.Interval, result.Err = historicalLiveSpan(step["value"])
				}
				future <- result
				close(future)
				return future
			}})
		}
		result := []any{}
		for span, err := range MergeSourceGeneratorIntervals(ctx, generators) {
			if err != nil {
				return nil, err
			}
			result = append(result, []any{span[0], span[1]})
		}
		expected, err := json.Marshal(args["calls"])
		if err != nil {
			return nil, err
		}
		actual, err := json.Marshal(calls)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(actual, expected) {
			return nil, fmt.Errorf("source merge next-call order differs: %s vs%s", actual, expected)
		}
		return result, nil
	}
	id, err := historicalLiveUint(args["instance"])
	if err != nil {
		return nil, err
	}
	instance, ok := c.instances[id]
	if !ok {
		return nil, fmt.Errorf("historical instance missing")
	}
	instance.rpc.load(call.Providers)
	required, factories, err := historicalLiveIntervals(args)
	if err != nil {
		return nil, err
	}
	var result any
	switch call.Operation {
	case "memory/syncBlockData":
		pages := []any{}
		for page, failure := range instance.memory.SyncBlockData(ctx, required, factories) {
			if failure != nil {
				return nil, failure
			}
			pages = append(pages, historicalLiveOutput(page))
		}
		result = pages
	case "persistent/syncBlockRangeData":
		span, failure := historicalLiveSpan(args["interval"])
		if failure != nil {
			return nil, failure
		}
		result, err = instance.persistent.SyncBlockRangeData(ctx, span, required, factories, c.database)
	case "persistent/syncBlockData":
		span, failure := historicalLiveSpan(args["interval"])
		if failure != nil {
			return nil, failure
		}
		logs, failure := historicalLiveObjects(args["logs"])
		if failure != nil {
			return nil, failure
		}
		block, failure := instance.persistent.SyncBlockData(ctx, span, required, logs, c.database)
		err = failure
		result = block
		if block == nil {
			result = filter.Undefined
		}
	default:
		return nil, fmt.Errorf("unknown historical method %s", call.Operation)
	}
	if err != nil {
		return nil, err
	}
	if err = instance.rpc.finished(); err != nil {
		return nil, err
	}
	if err = c.registry.compare(historicalLiveChildOutput(instance.children), call.Children, "children"); err != nil {
		return nil, err
	}
	return result, nil
}

// Complete original bodies/hooks/inputs/assertions execute separately from the
// independent native acquisition. Native type obligations still gate mappings.
func TestUpstreamSourceHistorical(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Tests []historicalLiveRecord `json:"tests"`
	}
	if err = historicalLiveReadJSON(root+"/parity/test-ledger.json", &ledger); err != nil {
		t.Fatal(err)
	}
	historicalRecords := []historicalLiveRecord{}
	for _, record := range ledger.Tests {
		if strings.Contains(record.File, "/sync-historical/") {
			historicalRecords = append(historicalRecords, record)
		}
	}
	controls, err := historicalLoadTypes(root, historicalRecords)
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			for _, kind := range []string{"in-memory", "index"} {
				if selected := os.Getenv("PONDER_HISTORICAL_FOCUS_KIND"); selected != "" && selected != kind {
					continue
				}
				t.Run(kind, func(t *testing.T) {
					states := map[string]*historicalLiveCase{}
					defer func() {
						for _, state := range states {
							_ = state.closeBackend()
						}
					}()
					order := []string{}
					file := "packages/core/src/sync-historical/" + kind + ".test.ts"
					raw, err := os.ReadFile(filepath.Join(root, "../ponder-upstream", file))
					if err != nil {
						t.Fatal(err)
					}
					hash := sha256.Sum256(raw)
					sourceHash := hex.EncodeToString(hash[:])
					for _, record := range ledger.Tests {
						if record.File != file {
							continue
						}
						if record.SourceSHA != sourceHash {
							t.Fatal("original historical source changed")
						}
						states[record.ID] = &historicalLiveCase{record: record, dialect: dialect, registry: newSourceLiveHistoricalRegistry(), instances: map[uint64]*historicalLiveInstance{}, rpcs: map[uint64]*historicalLiveReplay{}, actions: map[uint64]*rpc.Actions{}, children: map[uint64]*SourceHistoricalChildren{}, calls: []map[string]any{}, errors: []string{}}
						order = append(order, record.ID)
					}
					expected := 17
					if kind == "in-memory" {
						expected = 23
					}
					if len(states) != expected {
						t.Fatal("missing complete original historical inventory")
					}
					directory, err := os.MkdirTemp(root+"/parity/runs/history-m33", "live-"+dialect+"-"+kind+"-")
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("Live source historical evidence: %s", directory)
					var mu sync.Mutex
					var active *historicalLiveCase
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						fail := func(err error) {
							if active != nil {
								active.errors = append(active.errors, err.Error())
							}
							http.Error(w, err.Error(), 500)
						}
						var call historicalLiveRequest
						decoder := json.NewDecoder(r.Body)
						decoder.UseNumber()
						if err := decoder.Decode(&call); err != nil {
							fail(err)
							return
						}
						if r.URL.Path == "/start" {
							state, ok := states[call.CaseID]
							if !ok || state.complete {
								fail(fmt.Errorf("unknown or repeated original case"))
								return
							}
							active = state
							w.WriteHeader(http.StatusNoContent)
							return
						}
						if active == nil || active.record.ID != call.CaseID {
							fail(fmt.Errorf("historical case mismatch"))
							return
						}
						switch r.URL.Path {
						case "/call":
							if call.Sequence != len(active.calls) {
								fail(fmt.Errorf("historical sequence mismatch"))
								return
							}
							result, err := active.invoke(ctx, call)
							if err != nil {
								fail(fmt.Errorf("%s: %w", call.Operation, err))
								return
							}
							if err = active.registry.compare(historicalLiveOutput(result), call.Result, call.Operation); err != nil {
								fail(err)
								return
							}
							if call.Physical != nil {
								if err = historicalLiveComparePhysical(ctx, active.backend, active.cache, call.Physical); err != nil {
									fail(err)
									return
								}
							}
							active.calls = append(active.calls, map[string]any{"operation": call.Operation, "provider_inputs": len(call.Providers), "native_requests": historicalLiveNativeRequests(active, call.Operation), "physical_compared": call.Physical != nil})
						case "/case":
							if len(active.calls) == 0 {
								fail(fmt.Errorf("no native historical calls"))
								return
							}
							if call.Physical != nil {
								if err := historicalLiveComparePhysical(ctx, active.backend, active.cache, call.Physical); err != nil {
									fail(err)
									return
								}
							}
							active.complete = true
							if err := active.closeBackend(); err != nil {
								fail(err)
								return
							}
						case "/types-start":
							if !active.complete || !active.backendClosed || len(active.errors) > 0 || active.typesStarted {
								fail(fmt.Errorf("all original case comparisons and backend close must precede type programs"))
								return
							}
							active.typesStarted = true
						case "/types-run":
							if !active.typesStarted || !active.complete || !active.backendClosed || active.typesDone || call.SourceID != active.record.ID {
								fail(fmt.Errorf("historical positive program escaped original case/closed backend"))
								return
							}
							var selected *historicalTypeControl
							for i := range controls {
								control := &controls[i]
								if control.SourceID == call.SourceID && control.Index == call.DirectiveIndex {
									selected = control
									break
								}
							}
							if selected == nil {
								fail(fmt.Errorf("unknown exact historical type directive"))
								return
							}
							for _, run := range active.typeRuns {
								if run["directive_index"] == call.DirectiveIndex {
									fail(fmt.Errorf("duplicate historical type program"))
									return
								}
							}
							result, err := historicalExecuteType(ctx, root, dialect, *selected, call.Payload)
							if result != nil {
								active.typeRuns = append(active.typeRuns, result)
							}
							if err != nil {
								fail(err)
								return
							}
						case "/types-finish":
							if !active.typesStarted || active.typesDone || !active.backendClosed || len(active.typeRuns) != len(active.record.TypeDirectives) || len(active.errors) > 0 {
								fail(fmt.Errorf("complete exact historical type programs required"))
								return
							}
							for _, run := range active.typeRuns {
								if run["passed"] != true {
									fail(fmt.Errorf("historical type program failed"))
									return
								}
							}
							active.typesDone = true
						default:
							fail(fmt.Errorf("unknown historical bridge route"))
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					defer server.Close()
					command := exec.CommandContext(ctx, "node", root+"/tools/source-historical-original.mjs", "--backend="+dialect, "--kind="+kind, "--output="+directory)
					command.Dir = root
					command.Env = append(os.Environ(), "PONDER_HISTORICAL_ENDPOINT="+server.URL, "PONDER_PORT_ROOT="+root)
					output, runErr := command.CombinedOutput()
					if err = os.WriteFile(directory+"/parent-output.txt", output, 0600); err != nil {
						t.Fatal(err)
					}
					cases := []any{}
					for _, id := range order {
						state := states[id]
						cases = append(cases, map[string]any{"id": id, "title": state.record.FullName, "source_sha256": state.record.SourceSHA, "complete": state.complete, "comparison_backend_closed": state.backendClosed, "calls": state.calls, "errors": state.errors, "types_done": state.typesDone, "type_executions": state.typeRuns})
					}
					report := map[string]any{"state": "Actual full original historical public calls, raw provider replay, independent native acquisition/cache; native types/full acceptance pending, zero source credit", "backend": dialect, "kind": kind, "source_credit": 0, "cases": cases, "original_exit_error": fmt.Sprint(runErr)}
					if os.Getenv("PONDER_UPSTREAM_ROOT") != "" {
						report["environment_adapted"] = true
						report["pristine_file"] = false
						report["source_credit"] = 0
						report["state"] = "ENVIRONMENT-ADAPTED startup; NOT pristine. " + report["state"].(string)
					}
					encoded, _ := json.MarshalIndent(report, "", "  ")
					if err = os.WriteFile(directory+"/native.json", append(encoded, '\n'), 0600); err != nil {
						t.Fatal(err)
					}
					if runErr != nil {
						t.Fatalf("original historical bridge: %v\n%s\nEvidence:%s", runErr, output, directory)
					}
					for _, id := range order {
						state := states[id]
						t.Run(id, func(t *testing.T) {
							if !state.complete || len(state.errors) > 0 || len(state.calls) == 0 || !state.typesDone || len(state.typeRuns) != len(state.record.TypeDirectives) {
								t.Fatalf("incomplete original historical proof: %+v", state)
							}
						})
					}
				})
			}
		})
	}
}

func newSourceLiveHistoricalRegistry() *historicalLiveRegistry {
	return &historicalLiveRegistry{refs: map[int64]filter.Object{}}
}
func historicalLiveNativeRequests(c *historicalLiveCase, operation string) []string {
	result := []string{}
	if strings.HasPrefix(operation, "memory/") || strings.HasPrefix(operation, "persistent/") {
		for _, requester := range c.rpcs {
			requester.mu.Lock()
			result = append(result, requester.requests...)
			requester.mu.Unlock()
		}
	}
	return result
}
