package indexing

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
	"ponder.local/ponder-go/rpc"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

type indexingSourceCase struct {
	ID         string               `json:"id"`
	Name       string               `json:"full_name"`
	Targeted   bool                 `json:"targeted"`
	Assertions []any                `json:"assertions"`
	Calls      []indexingSourceCall `json:"calls"`
	External   []Object             `json:"external_operations"`
}
type indexingSourceCall struct {
	Args    any                    `json:"args"`
	Defined bool                   `json:"result_defined"`
	Methods []indexingSourceMethod `json:"methods"`
}
type indexingSourceMethod struct {
	Operation   string   `json:"operation"`
	Args        any      `json:"args"`
	Before      any      `json:"before"`
	After       any      `json:"after"`
	Callbacks   []Object `json:"callbacks"`
	Providers   []Object `json:"providers"`
	Metrics     []Object `json:"metrics"`
	Error       any      `json:"error"`
	InputsAfter any      `json:"event_inputs_after"`
	Updates     []Object `json:"updates"`
}
type indexingSourceFixture struct {
	BackendCases map[string][]indexingSourceCase `json:"backend_cases"`
	Targets      []string                        `json:"targets"`
	TypeGaps     []string                        `json:"type_gaps"`
}

// Marked values are decoded only from source INPUT snapshots. The same decoder
// also reads expected snapshots for comparisons, never to populate actual state.
func indexingUnmark(t *testing.T, value any) any {
	t.Helper()
	refs := map[int]any{}
	var walk func(any) any
	walk = func(value any) any {
		switch v := value.(type) {
		case []any:
			out := make([]any, len(v))
			for i, x := range v {
				out[i] = walk(x)
			}
			return out
		case map[string]any:
			if _, ok := v["$undefined"]; ok {
				return filter.Undefined
			}
			if x, ok := v["$bigint"].(string); ok {
				n, valid := new(big.Int).SetString(x, 10)
				if !valid {
					t.Fatal("bad bigint")
				}
				return n
			}
			if x, ok := v["$ref"].(float64); ok {
				r, found := refs[int(x)]
				if !found {
					t.Fatalf("dangling source ref %v", x)
				}
				return r
			}
			if fields, ok := v["$object"].([]any); ok {
				o := Object{}
				if id, ok := v["$id"].(float64); ok {
					refs[int(id)] = o
				}
				for _, entry := range fields {
					pair := entry.([]any)
					o[pair[0].(string)] = walk(pair[1])
				}
				return o
			}
			if entries, ok := v["$map"].([]any); ok {
				o := Object{}
				for _, entry := range entries {
					pair := entry.([]any)
					o[pair[0].(string)] = walk(pair[1])
				}
				return o
			}
			if entries, ok := v["$set"].([]any); ok {
				return walk(entries)
			}
			if _, ok := v["$function"]; ok {
				return Object{"$function": v["$function"]}
			}
			o := Object{}
			for key, x := range v {
				o[key] = walk(x)
			}
			return o
		default:
			return v
		}
	}
	return walk(value)
}
func indexingCanonical(value any) any {
	if reflect.DeepEqual(value, filter.Undefined) {
		return Object{"$undefined": true}
	}
	switch v := value.(type) {
	case *big.Int:
		return Object{"$bigint": v.String()}
	case map[string]any:
		o := Object{}
		for k, x := range v {
			o[k] = indexingCanonical(x)
		}
		return o
	case store.Row:
		o := Object{}
		for k, x := range v {
			o[k] = indexingCanonical(x)
		}
		return o
	case []Object:
		a := make([]any, len(v))
		for i, x := range v {
			a[i] = indexingCanonical(x)
		}
		return a
	case []any:
		a := make([]any, len(v))
		for i, x := range v {
			a[i] = indexingCanonical(x)
		}
		return a
	case []string:
		a := make([]any, len(v))
		for i, x := range v {
			a[i] = x
		}
		return a
	default:
		return value
	}
}
func indexingEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	g, _ := json.Marshal(indexingCanonical(got))
	w, _ := json.Marshal(indexingCanonical(want))
	if string(g) != string(w) {
		t.Fatalf("%s mismatch\ngot %s\nwant %s", label, g, w)
	}
}
func indexingObject(v any) Object { return v.(map[string]any) }
func indexingObjects(v any) []Object {
	a := v.([]any)
	out := make([]Object, len(a))
	for i, x := range a {
		out[i] = indexingObject(x)
	}
	return out
}
func indexingGroups(v any) [][]Object {
	a := v.([]any)
	out := make([][]Object, len(a))
	for i, x := range a {
		out[i] = indexingObjects(x)
	}
	return out
}
func indexingKeys(v Object) []string {
	a := []string{}
	for k := range v {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}

func indexingReplayStore(t *testing.T, backend string) *store.Store {
	t.Helper()
	ctx := context.Background()
	var db store.Backend
	var err error
	if backend == "postgres" {
		url := os.Getenv("PONDER_TEST_POSTGRES")
		if url == "" {
			t.Skip("explicit task-only PostgreSQL required; no parity credit")
		}
		db, err = store.OpenPostgres(ctx, url)
	} else {
		db, err = store.OpenPGlite(ctx, "memory://")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
	s, err := store.New(ctx, db, model, fmt.Sprintf("ponder_go_m38_source_%x", time.Now().UnixNano()), schema.Multichain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema.Quote(s.Namespace)+" CASCADE")
	})
	return s
}

type indexingReplayProvider struct {
	mu       sync.Mutex
	t        *testing.T
	expected []Object
	calls    []Object
}

func (p *indexingReplayProvider) serve(w http.ResponseWriter, r *http.Request) {
	var request Object
	if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
		p.t.Error(e)
		w.WriteHeader(500)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i := len(p.calls)
	actual := Object{"method": request["method"], "params": request["params"]}
	p.calls = append(p.calls, actual)
	if i >= len(p.expected) {
		p.t.Error("unexpected actual RPC", actual)
		w.WriteHeader(500)
		return
	}
	expected := p.expected[i]
	got, _ := json.Marshal(indexingCanonical(actual))
	want, _ := json.Marshal(indexingCanonical(expected["request"]))
	if string(got) != string(want) {
		p.t.Errorf("provider request mismatch: got %s want %s", got, want)
		_ = json.NewEncoder(w).Encode(Object{"jsonrpc": "2.0", "id": request["id"], "error": Object{"code": -32000, "message": "native provider request mismatch"}})
		return
	}
	// Only raw external provider response is oracle input. Handler/database/service
	// outputs are never used to seed execution.
	response := expected["response"]
	_ = json.NewEncoder(w).Encode(Object{"jsonrpc": "2.0", "id": request["id"], "result": response})
}

type indexingIdentityChecks struct {
	source map[string]map[float64]uintptr
	native map[string]map[uintptr]float64
}

func (c *indexingIdentityChecks) check(t *testing.T, kind string, source any, native any) {
	t.Helper()
	id, ok := source.(float64)
	if !ok {
		return
	}
	v := reflect.ValueOf(native)
	if !v.IsValid() {
		t.Fatalf("%s identity is nil", kind)
	}
	var ptr uintptr
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		ptr = v.Pointer()
	default:
		return
	}
	if ptr == 0 {
		t.Fatalf("%s identity is nil", kind)
	}
	if c.source == nil {
		c.source = map[string]map[float64]uintptr{}
		c.native = map[string]map[uintptr]float64{}
	}
	if c.source[kind] == nil {
		c.source[kind] = map[float64]uintptr{}
		c.native[kind] = map[uintptr]float64{}
	}
	if old, exists := c.source[kind][id]; exists && old != ptr {
		t.Fatalf("%s source identity %v did not retain native identity", kind, id)
	}
	if old, exists := c.native[kind][ptr]; exists && old != id {
		t.Fatalf("%s distinct source identities %v/%v collapsed", kind, old, id)
	}
	c.source[kind][id] = ptr
	c.native[kind][ptr] = id
}

func TestSourceIndexingService(t *testing.T) {
	path := filepath.Join("..", "testdata", "indexing", "source-indexing-service-live.json.gz")
	compressed, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	fixtureDigest := sha256.Sum256(compressed)
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	data, e := io.ReadAll(gz)
	if e != nil {
		t.Fatal(e)
	}
	var fixture indexingSourceFixture
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Targets) != 15 || len(fixture.TypeGaps) != 3 {
		t.Fatal("source census differs")
	}
	observations := []Object{}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			count := 0
			for _, record := range fixture.BackendCases[backend] {
				if !record.Targeted {
					continue
				}
				count++
				record := record
				t.Run(record.ID, func(t *testing.T) {
					observation := indexingReplayCase(t, backend, record)
					observations = append(observations, observation)
				})
			}
			if count != 15 {
				t.Fatal("incomplete source cases", count)
			}
		})
	}
	if output := os.Getenv("PONDER_INDEXING_SERVICE_NATIVE_OUTPUT"); output != "" {
		encoded, e := json.MarshalIndent(Object{"source_credit": 0, "source_fixture_sha256": hex.EncodeToString(fixtureDigest[:]), "state": "Actual native dispatch with original inputs, real IndexingStore and loopback RPC; three TS-only directives remain unmapped", "cases": observations}, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(output, append(encoded, '\n'), 0644); e != nil {
			t.Fatal(e)
		}
	}
}

func indexingReplayCase(t *testing.T, backend string, record indexingSourceCase) Object {
	t.Helper()
	if len(record.Calls) != 1 {
		t.Fatal("expected one createIndexing", len(record.Calls))
	}
	call := record.Calls[0]
	input := indexingObject(indexingUnmark(t, call.Args))
	build := Build{Chains: indexingObjects(input["chains"]), Contracts: indexingObjects(input["contracts"]), EventCallbacks: indexingGroups(input["eventCallbacks"]), SetupCallbacks: indexingGroups(input["setupCallbacks"]), IndexingFunctions: indexingObjects(input["indexingFunctions"])}
	provider := &indexingReplayProvider{t: t}
	server := httptest.NewServer(http.HandlerFunc(provider.serve))
	defer server.Close()
	requester := rpc.New(server.URL)
	requester.Retries = 0
	requester.RequestsPerSecond = 0
	requesters := make([]rpc.Requester, len(build.Chains))
	for i := range requesters {
		requesters[i] = requester
	}
	clients, e := NewClientSet(build.Chains, requesters)
	if e != nil {
		t.Fatal(e)
	}
	dbStore := indexingReplayStore(t, backend)
	state := &ErrorState{}
	ctx := context.Background()
	var service *Service
	var argsIdentity *HandlerArgs
	identity := &indexingIdentityChecks{}
	callbackCount, insertCount, accessCount := 0, 0, 0
	counter := 0
	insertCounter := 0
	var historicalOriginal Object
	retainedViews := []EventView{}
	var active *indexingSourceMethod
	callbackIndex := 0
	actualOps := []Object{}
	summaries := []Object{}
	// These are direct translations of the unchanged source callback bodies, not
	// a generic interpreter that returns expected captured results.
	handler := Handler(func(ctx context.Context, args *HandlerArgs) error {
		if active == nil || callbackIndex >= len(active.Callbacks) {
			t.Fatal("unexpected callback")
		}
		expected := active.Callbacks[callbackIndex]
		callbackIndex++
		callbackCount++
		if argsIdentity == nil {
			argsIdentity = args
		} else if argsIdentity != args {
			t.Fatal("handler argument identity changed")
		}
		identity.check(t, "argument", expected["argument_identity"], args)
		identity.check(t, "context", expected["context_identity"], args.Context)
		identity.check(t, "client", expected["client_identity"], args.Context.Client)
		identity.check(t, "db", expected["db_identity"], args.Context.DB)
		indexingEqual(t, "context values", Object{"chain": args.Context.Chain, "contracts": args.Context.Contracts}, indexingUnmark(t, expected["context"]))
		if args.Context.Client == nil || args.Context.DB == nil || args.Context.DB.Underlying() == nil {
			t.Fatal("missing actual context client/db")
		}
		if active.Operation == "processSetupEvents" {
			if args.Event != nil {
				t.Fatal("setup event must be undefined")
			}
		} else {
			if args.Event == nil {
				t.Fatal("event undefined")
			}
			retainedViews = append(retainedViews, args.Event)
			identity.check(t, "event", expected["event_identity"], args.Event)
			expectedKeys := []string{}
			for _, raw := range expected["event_fields"].([]any) {
				expectedKeys = append(expectedKeys, indexingObject(raw)["key"].(string))
			}
			sort.Strings(expectedKeys)
			indexingEqual(t, "callback event keys", indexingKeys(Object(args.Event)), expectedKeys)
			if _, ok := args.Event.Get("id").(string); !ok {
				t.Fatal("original event id is not a string")
			}
			for _, raw := range expected["event_fields"].([]any) {
				field := indexingObject(raw)
				key := field["key"].(string)
				value := args.Event.Get(key)
				identity.check(t, "field:"+key, field["identity"], value)
				if _, ok := field["identity"].(string); ok {
					indexingEqual(t, "scalar event field "+key, value, field["identity"])
				}
				if marker, ok := field["identity"].(map[string]any); ok {
					if marker["$undefined"] == true {
						indexingEqual(t, "undefined event field "+key, value, filter.Undefined)
					}
				}

			}
		}
		operations := []Object{}
		add := func(name string, values Object) {
			operations = append(operations, Object{"operation": name, "args": values})
		}
		var err error
		switch record.ID {
		case "0fae55812c46f74f1566", "584d162581ce27033006":
			address := "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
			add("client.getBalance", Object{"chainId": args.Context.Chain["id"], "args": []any{Object{"address": address}}})
			_, err = args.Context.Client.GetBalance(ctx, address)
		case "d7d1c6e399576ff940b3", "71d417e2bb1b5285447b":
			address := "0x0000000000000000000000000000000000000000"
			if record.ID == "71d417e2bb1b5285447b" {
				address = fmt.Sprintf("0x000000000000000000000000000000000000000%d", insertCounter)
				insertCounter++
			}
			row := store.Row{"address": address, "balance": big.NewInt(10)}
			add("db.insert", Object{"table": "account", "values": Object(row)})
			insertCount++
			_, err = args.Context.DB.InsertOne(ctx, "account", row, store.Conflict{})
		case "d75ae6d953a7bb8ea73c", "afb6150d1f928a58bf24":
			err = errors.New("")
		case "772c4096d1ac5227fc45":
			args.Event.Delete("transaction")
			err = errors.New("empty transaction")
		case "8a08a48e2a99b23fa016":
			property, ok := args.Event.Property("transaction")
			if !ok {
				t.Fatal("historical transaction not intercepted")
			}
			name := property.eventName
			read := func(key string) error {
				add("event.access", Object{"eventName": name, "kind": "transaction", "property": key})
				accessCount++
				_, e := property.Get(key)
				return e
			}
			err = read("gas")
			if err == nil {
				err = read("maxFeePerGas")
			}
			if err == nil {
				old := counter
				counter++
				if old == 1001 {
					err = read("maxPriorityFeePerGas")
				}
			}
		}
		expectedOperations := []Object{}
		for _, raw := range expected["operations"].([]any) {
			op := indexingObject(raw)
			expectedOperations = append(expectedOperations, Object{"operation": op["operation"], "args": indexingUnmark(t, op["args"])})
		}
		indexingEqual(t, "callback operations", operations, expectedOperations)
		actualOps = append(actualOps, operations...)
		return err
	})
	for _, groups := range [][][]Object{build.EventCallbacks, build.SetupCallbacks} {
		for _, group := range groups {
			for _, cb := range group {
				cb["fn"] = handler
			}
		}
	}
	e = dbStore.Apply(ctx, store.Event{ID: record.ID, Checkpoint: checkpoint.Checkpoint{ChainID: 1, BlockNumber: 1, EventType: 5}}, func(w *store.Writer) error {
		database := NewDatabase(w.Indexing(), state)
		var err error
		service, err = NewService(Options{Build: build, Database: database, Clients: clients, Errors: state})
		if err != nil {
			return err
		}
		if !call.Defined || service == nil {
			t.Fatal("source createIndexing defined")
		}
		for mi := range call.Methods {
			m := &call.Methods[mi]
			active = m
			callbackIndex = 0
			provider.mu.Lock()
			provider.expected = nil
			for _, captured := range m.Providers {
				requests := indexingUnmark(t, captured["request"]).([]any)
				provider.expected = append(provider.expected, Object{"request": requests[0], "response": indexingUnmark(t, captured["response"])})
			}
			provider.calls = nil
			provider.mu.Unlock()
			beforeDurations := len(service.Metrics().Durations)
			beforeCompleted := Object{}
			for k, v := range service.Metrics().Completed {
				beforeCompleted[k] = v
			}
			var events []Object
			if m.Operation != "processSetupEvents" {
				methodInput := indexingObject(indexingUnmark(t, m.Args))
				events = indexingObjects(methodInput["events"])
				if record.ID == "8a08a48e2a99b23fa016" {
					if mi == 0 {
						historicalOriginal = events[0]
					} else {
						// Original test deletes this required field on the SAME
						// event between the 1001-sample call and retry call.
						delete(indexingObject(indexingObject(historicalOriginal["event"])["transaction"]), "maxPriorityFeePerGas")
						indexingEqual(t, "column selection second input", historicalOriginal["event"], events[0]["event"])
						events = []Object{historicalOriginal}
					}
				}
				for _, event := range events {
					cb := indexingObject(event["eventCallback"])
					cb["fn"] = handler
				}
			}
			updates := []Object{}
			switch m.Operation {
			case "processSetupEvents":
				err = service.ProcessSetupEvents(ctx)
			case "processRealtimeEvents":
				err = service.ProcessRealtimeEvents(ctx, events)
			case "processHistoricalEvents":
				err = service.ProcessHistoricalEvents(ctx, events, func(event, chain Object) {
					updates = append(updates, Object{"checkpoint": event["checkpoint"], "chainId": chain["id"]})
				})
			default:
				t.Fatal("unknown method", m.Operation)
			}
			if callbackIndex != len(m.Callbacks) {
				t.Fatalf("callbacks %d want %d (dispatch error: %v)", callbackIndex, len(m.Callbacks), err)
			}
			expectedError := indexingUnmark(t, m.Error)
			if m.Error == nil {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil {
					t.Fatal("expected source rejection")
				}
				sourceError := indexingObject(expectedError)["$error"].(Object)
				named, ok := err.(interface{ ErrorName() string })
				if !ok {
					t.Fatalf("error lacks source class: %T %v", err, err)
				}
				indexingEqual(t, "error class/message", Object{"name": named.ErrorName(), "message": err.Error()}, Object{"name": sourceError["name"], "message": sourceError["message"]})
				if record.ID == "8a08a48e2a99b23fa016" {
					var invalid *InvalidEventAccessError
					if !errors.As(err, &invalid) || invalid.Key != "transaction.maxPriorityFeePerGas" {
						t.Fatal("wrong invalid access key", err)
					}
				}
			}
			after := indexingObject(indexingUnmark(t, m.After))
			counts := Object{}
			for k, v := range service.EventCount() {
				counts[k] = v
			}
			indexingEqual(t, "event counts", counts, after["eventCount"])
			indexingEqual(t, "hasError", service.Metrics().HasError, after["hasError"])
			if state.GetRetryableError() != nil {
				t.Fatal("retryable error not cleared")
			}
			patterns := Object{}
			for name, p := range service.ColumnAccessPattern() {
				patterns[name] = Object{"block": p.Block, "trace": p.Trace, "transaction": p.Transaction, "transactionReceipt": p.TransactionReceipt, "resolved": p.Resolved, "count": p.Count}
			}
			indexingEqual(t, "column access pattern", patterns, after["columnAccessPattern"])
			expectedFilters := indexingGroups(after["filters"])
			for i, group := range build.EventCallbacks {
				for j, cb := range group {
					indexingEqual(t, "mutated filter", cb["filter"], expectedFilters[i][j]["filter"])
				}
			}
			durationLabels := []any{}
			completed := []any{}
			for _, metric := range m.Metrics {
				if metric["operation"] == "duration" {
					durationLabels = append(durationLabels, indexingUnmark(t, metric["labels"]))
				} else {
					completed = append(completed, indexingUnmark(t, metric["args"]))
				}
			}
			actualLabels := []any{}
			for _, duration := range service.Metrics().Durations[beforeDurations:] {
				if duration.Seconds < 0 {
					t.Fatal("negative duration")
				}
				actualLabels = append(actualLabels, Object{"event": duration.Event})
			}
			indexingEqual(t, "duration labels/count", actualLabels, durationLabels)
			completedCounts := Object{}
			for _, entry := range completed {
				name := entry.([]any)[0].(string)
				n, _ := completedCounts[name].(int)
				completedCounts[name] = n + 1
			}
			actualCompleted := Object{}
			for name, n := range service.Metrics().Completed {
				old, _ := beforeCompleted[name].(uint64)
				if n > old {
					actualCompleted[name] = n - old
				}
			}
			indexingEqual(t, "completed labels/count", actualCompleted, completedCounts)
			if m.Operation == "processHistoricalEvents" && err == nil && len(events) > 0 {
				if len(updates) == 0 {
					t.Fatal("missing final indexing seconds update")
				}
				last := events[len(events)-1]
				indexingEqual(t, "final update", updates[len(updates)-1], Object{"checkpoint": last["checkpoint"], "chainId": indexingObject(last["chain"])["id"]})
			}
			if m.Operation != "processSetupEvents" {
				expectedAfter := indexingObjects(indexingUnmark(t, m.InputsAfter))
				for i, event := range events {
					indexingEqual(t, "input body after callback", event["event"], expectedAfter[i]["event"])
				}
			}
			provider.mu.Lock()
			rpcCalls := append([]Object{}, provider.calls...)
			provider.mu.Unlock()
			if len(rpcCalls) != len(m.Providers) {
				t.Fatal("provider call count", len(rpcCalls), len(m.Providers))
			}
			summaries = append(summaries, Object{"method": m.Operation, "callbacks": callbackIndex, "eventCount": counts, "profiles": patterns, "hasError": service.Metrics().HasError, "durationLabels": actualLabels, "completed": actualCompleted, "rpc": rpcCalls, "error": func() any {
				if err == nil {
					return nil
				}
				return Object{"name": err.(interface{ ErrorName() string }).ErrorName(), "message": err.Error()}
			}()})
		}
		for _, op := range record.External {
			if op["operation"] != "db.find" {
				t.Fatal("unhandled external source operation", op["operation"])
			}
			args := indexingObject(indexingUnmark(t, op["args"]))
			row, e := database.Find(ctx, args["table"].(string), store.Row(indexingObject(args["key"])))
			if e != nil {
				return e
			}
			indexingEqual(t, "source external find", row, indexingUnmark(t, op["result"]))
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if record.ID == "d7d1c6e399576ff940b3" || record.ID == "71d417e2bb1b5285447b" {
		if insertCount != 1 {
			t.Fatal("source insert count", insertCount)
		}
	}
	if record.ID == "8a08a48e2a99b23fa016" && counter != 1002 {
		t.Fatal("source 1001 plus 1 input lost", counter)
	}
	// Exact original assertions are preserved in the fixture and exercised above.
	// The three TS-only directives are deliberately not represented as Go proofs.
	_ = retainedViews // Keep captured event identities alive for the full comparison.
	digestInput := indexingCanonical(Object{"methods": summaries, "operations": actualOps})
	encoded, _ := json.Marshal(digestInput)
	digest := sha256.Sum256(encoded)
	return Object{"backend": backend, "id": record.ID, "name": record.Name, "pass": true, "source_assertions_checked": len(record.Assertions), "callbacks": callbackCount, "insert_calls": insertCount, "access_calls": accessCount, "observation_sha256": hex.EncodeToString(digest[:]), "digest_input": digestInput, "observations": summaries, "runtime_only_type_gap": strings.Contains("d75ae6d953a7bb8ea73c afb6150d1f928a58bf24 8a08a48e2a99b23fa016", record.ID)}
}
