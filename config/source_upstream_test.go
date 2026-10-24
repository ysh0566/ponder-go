package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
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
)

type sourceTypedControl struct {
	SourceID string `json:"source_id"`
	Mode     string `json:"mode"`
	Input    any    `json:"input"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

type sourceLiveCall struct {
	TypedControls             []sourceTypedControl            `json:"typed_controls,omitempty"`
	MetadataIdentityChecks    int                             `json:"metadata_identity_checks"`
	HyperliquidIdentityChecks int                             `json:"hyperliquid_identity_checks"`
	TypeProof                 any                             `json:"type_proof,omitempty"`
	RPCIdentityChecks         int                             `json:"rpc_identity_checks"`
	CallbackIdentityChecks    int                             `json:"callback_identity_checks"`
	CaseID                    string                          `json:"case_id"`
	Operation                 string                          `json:"operation"`
	Args                      json.RawMessage                 `json:"args"`
	ArgsAfter                 json.RawMessage                 `json:"args_after"`
	Result                    json.RawMessage                 `json:"result"`
	Error                     *struct{ Name, Message string } `json:"error"`
	NativeResult              any                             `json:"native_result"`
	NativeAfter               any                             `json:"native_after"`
	NativeError               string                          `json:"native_error,omitempty"`
	Differences               []string                        `json:"differences"`
}

type sourceLiveCase struct {
	ID, Title           string
	Parameter           any
	TypeErrorDirectives []any `json:"type_error_directives"`
	Calls               []sourceLiveCall
}

// sourceComparable preserves explicit undefined/null and all comparable fields.
// RPC methods and functions are opaque; actual native clients are associated
// with source RPC identity IDs, and callback chain/function identities checked.
func sourceComparable(value any) any {
	if filter.IsUndefined(value) {
		return map[string]any{"$undefined": true}
	}
	switch v := value.(type) {
	case *sourceObject:
		out := map[string]any{}
		for _, key := range v.keys {
			out[key] = sourceComparable(v.get(key))
		}
		return out
	case sourceFunctionRef:
		return map[string]any{"$function": true}
	case sourceRPCRef:
		return map[string]any{"$rpc": map[string]any{"hostnames": v.Hostnames}}
	case *big.Int:
		return map[string]any{"$bigint": v.String()}
	case float64:
		if math.IsNaN(v) {
			return map[string]any{"$number": "NaN"}
		}
		if math.IsInf(v, 1) {
			return map[string]any{"$number": "+Infinity"}
		}
		if math.IsInf(v, -1) {
			return map[string]any{"$number": "-Infinity"}
		}
	}
	rv := reflect.ValueOf(value)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		out := []any{}
		for i := 0; i < rv.Len(); i++ {
			out = append(out, sourceComparable(rv.Index(i).Interface()))
		}
		return out
	}
	if rv.IsValid() && rv.Kind() == reflect.Map {
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = sourceComparable(iter.Value().Interface())
		}
		return out
	}
	return value
}
func sourceCanonical(value any) string {
	data, err := json.Marshal(sourceComparable(value))
	if err != nil {
		return "marshal error: " + err.Error()
	}
	return string(data)
}

func TestUpstreamConfigRuntime(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	for _, directory := range []string{root, filepath.Join(root, "../ponder-upstream"), filepath.Join(root, "../ponder-upstream/packages/core")} {
		if _, err := os.Stat(filepath.Join(directory, ".env.local")); !os.IsNotExist(err) {
			t.Fatal("Unexpected .env.local or unreadable source environment:", directory, err)
		}
	}
	revision := exec.Command("git", "rev-parse", "HEAD")
	revision.Dir = filepath.Join(root, "../ponder-upstream")
	if raw, err := revision.Output(); err != nil || strings.TrimSpace(string(raw)) != "17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334" {
		t.Fatal("Wrong original revision", err)
	}
	pristine := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	pristine.Dir = revision.Dir
	if raw, err := pristine.Output(); err != nil || strings.TrimSpace(string(raw)) != "" {
		t.Fatal("Original tracked files changed", err)
	}
	output, err := os.MkdirTemp(filepath.Join(root, "parity/runs/config-types-m29"), "live-")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := []sourceLiveCall{}
	cases := []sourceLiveCase{}
	builds := map[int64]*SourceConfigBuild{}
	clients := map[int64]*rpc.Client{}
	compile := exec.Command("node", filepath.Join(root, "tools/config-compile-fixtures.mjs"), filepath.Join(output, "block-filter-compile.json"))
	compile.Dir = root
	if raw, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("Public filter compiler proof: %v\n%s", err, raw)
	}

	typedCompile := exec.Command("node", filepath.Join(root, "tools/config-typed-compile-fixtures.mjs"), filepath.Join(output, "typed-compile.json"))
	typedCompile.Dir = root
	if raw, err := typedCompile.CombinedOutput(); err != nil {
		t.Fatalf("Production typed compiler controls: %v\n%s", err, raw)
	}
	typedRaw, err := os.ReadFile(filepath.Join(output, "typed-compile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var typedProof struct {
		Directives []struct {
			SourceID       string `json:"source_id"`
			Mode           string
			PositiveBinary string `json:"positive_binary"`
		}
	}
	if err := json.Unmarshal(typedRaw, &typedProof); err != nil {
		t.Fatal(err)
	}

	literalCompile := exec.Command("node", filepath.Join(root, "tools/config-literal-compile-fixtures.mjs"), filepath.Join(output, "literal-compile.json"))
	literalCompile.Dir = root
	if raw, err := literalCompile.CombinedOutput(); err != nil {
		t.Fatalf("Actual generated literal compiler controls: %v\n%s", err, raw)
	}
	literalRaw, err := os.ReadFile(filepath.Join(output, "literal-compile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var literalProof struct {
		Directives []struct {
			SourceID       string `json:"source_id"`
			Mode           string
			PositiveBinary string `json:"positive_binary"`
		}
	}
	if err := json.Unmarshal(literalRaw, &literalProof); err != nil {
		t.Fatal(err)
	}
	typedProof.Directives = append(typedProof.Directives, literalProof.Directives...)
	if len(typedProof.Directives) != 9 {
		t.Fatal("Expected all nine production typed/literal compiler controls")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.URL.Path == "/case" {
			var c sourceLiveCase
			if err := json.Unmarshal(body, &c); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			c.Calls = nil
			cases = append(cases, c)
			w.WriteHeader(204)
			return
		}
		var call sourceLiveCall
		if err := json.Unmarshal(body, &call); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		restored, err := sourceDecodeWire(call.Args)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		args := restored.(*sourceObject)
		cfg := &SourceConfiguration{value: args.get("config").(*sourceObject)}
		var result any
		var runError error
		checkMetadataIdentity := func(chains []*sourceObject) {
			catalog, err := loadSourceCatalog()
			if err != nil {
				call.Differences = append(call.Differences, err.Error())
				return
			}
			for _, chain := range chains {
				canonical := catalog[sourceString(chain.get("id"))]
				if canonical == nil {
					continue
				}
				call.MetadataIdentityChecks++
				if chain.get("viemChain") != canonical {
					call.Differences = append(call.Differences, "chain metadata is not the canonical native catalog object")
				}
				if sourceString(chain.get("id")) == "999" {
					call.HyperliquidIdentityChecks++
				}
			}
		}

		if call.Operation == "buildConfig" || call.Operation == "safeBuildConfig" {
			common := args.get("common").(*sourceObject)
			options := common.get("options").(*sourceObject)
			max, _ := sourceNumber(options.get("rpcMaxConcurrency"))
			var built *SourceConfigBuild
			built, runError = BuildSourceConfig(cfg, SourceBuildOptions{RPCMaxConcurrency: int(max)})
			if runError == nil {
				checkMetadataIdentity(built.value.Chains)
				result = built.Value()
				expected, decodeErr := sourceDecodeWire(call.Result)
				if decodeErr != nil {
					runError = decodeErr
				} else if object, ok := expected.(*sourceObject); ok {
					refs, _ := object.get("rpcs").([]any)
					for i, ref := range refs {
						builds[ref.(sourceRPCRef).ID] = built
						clients[ref.(sourceRPCRef).ID] = built.value.RPCs[i]
					}
				}
			}
		} else {
			rawBuild := args.get("configBuild").(*sourceObject)
			refs := rawBuild.get("rpcs").([]any)
			var built *SourceConfigBuild
			if len(refs) > 0 {
				built = builds[refs[0].(sourceRPCRef).ID]
			}
			if built != nil {
				for i, ref := range refs {
					if clients[ref.(sourceRPCRef).ID] != built.value.RPCs[i] {
						call.Differences = append(call.Differences, "source RPC identity mismatch")
					}
					call.RPCIdentityChecks++
				}
			}
			if built == nil {
				runError = fmt.Errorf("source RPC reference is not associated with an actual native build")
			} else {
				functions := []SourceIndexingFunction{}
				for _, value := range args.get("indexingFunctions").([]any) {
					f := value.(*sourceObject)
					functions = append(functions, SourceIndexingFunction{Name: sourceString(f.get("name")), Fn: f.get("fn")})
				}
				var indexing *SourceIndexingBuild
				indexing, runError = BuildSourceIndexing(r.Context(), cfg, built, functions)
				if runError == nil {
					checkMetadataIdentity(indexing.chains)
					result = indexing.Value()
					functionByName := map[string]any{}
					for _, f := range functions {
						functionByName[f.Name] = f.Fn
					}
					for ci, callbacks := range indexing.callbacks {
						for _, callback := range callbacks {
							call.CallbackIdentityChecks++
							if callback.get("fn") != functionByName[sourceString(callback.get("name"))] {
								call.Differences = append(call.Differences, "callback function identity differs")
							}
							if callback.get("chain") != indexing.chains[ci] {
								call.Differences = append(call.Differences, "callback chain identity differs")
							}
						}
					}
				}
			}
		}

		if call.Operation == "buildIndexingFunctions" && call.Error == nil {
			if blocks, ok := cfg.value.get("blocks").(*sourceObject); ok && len(blocks.keys) == 1 && blocks.has("a") {
				source := blocks.get("a").(*sourceObject)
				if sourceString(source.get("startBlock")) == "16370000" && sourceString(source.get("endBlock")) == "16370020" {
					input, _ := json.Marshal(cfg.Value())
					positive := exec.CommandContext(r.Context(), filepath.Join(root, ".parity-build/config-types/block-filter-positive"))
					positive.Stdin = bytes.NewReader(input)
					raw, err := positive.CombinedOutput()
					call.TypeProof = map[string]any{"input": sourceComparable(cfg.value), "positive_output": string(raw), "passed": err == nil, "original_directive": "filter.interval read requires narrowing the public Filter union"}
					if err != nil {
						call.Differences = append(call.Differences, fmt.Sprintf("actual block union positive failed: %v %s", err, raw))
					}
				}
			}
		}

		if call.Operation == "safeBuildIndexingFunctions" {
			for _, directive := range typedProof.Directives {
				if directive.SourceID != call.CaseID {
					continue
				}
				input, err := json.Marshal(cfg.Value())
				if err != nil {
					call.Differences = append(call.Differences, err.Error())
					continue
				}
				command := exec.CommandContext(r.Context(), directive.PositiveBinary)
				command.Stdin = bytes.NewReader(input)
				raw, runErr := command.CombinedOutput()
				exit := 0
				message := ""
				if runErr != nil {
					exit = -1
					if failure, ok := runErr.(*exec.ExitError); ok {
						exit = failure.ExitCode()
					}
					message = runErr.Error()
					call.Differences = append(call.Differences, "actual typed positive control failed: "+directive.Mode+": "+message+" "+string(raw))
				}
				call.TypedControls = append(call.TypedControls, sourceTypedControl{SourceID: directive.SourceID, Mode: directive.Mode, Input: sourceComparable(cfg.value), ExitCode: exit, Output: string(raw), Error: message})
			}
		}
		safe := strings.HasPrefix(call.Operation, "safe")
		if safe {
			if runError != nil {
				result = map[string]any{"status": "error", "error": map[string]any{"name": "BuildError", "message": runError.Error(), "stack": filter.Undefined}}
			} else {
				out := result.(map[string]any)
				out["status"] = "success"
			}
		}
		call.NativeResult = sourceComparable(result)
		call.NativeAfter = sourceComparable(cfg.value)
		if runError != nil {
			call.NativeError = runError.Error()
		}
		if call.Error != nil {
			if runError == nil || runError.Error() != call.Error.Message {
				call.Differences = append(call.Differences, fmt.Sprintf("error mismatch: native %v, source %s", runError, call.Error.Message))
			}
		} else {
			expected, err := sourceDecodeWire(call.Result)
			if err != nil {
				call.Differences = append(call.Differences, err.Error())
			} else if sourceCanonical(result) != sourceCanonical(expected) {
				call.Differences = append(call.Differences, "full result mismatch")
			}
			if !safe && runError != nil {
				call.Differences = append(call.Differences, "unexpected native error: "+runError.Error())
			}
		}
		after, err := sourceDecodeWire(call.ArgsAfter)
		if err != nil {
			call.Differences = append(call.Differences, err.Error())
		} else if sourceCanonical(cfg.value) != sourceCanonical(after.(*sourceObject).get("config")) {
			call.Differences = append(call.Differences, "configuration mutation mismatch")
		}
		calls = append(calls, call)
		w.WriteHeader(204)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oracle := filepath.Join(output, "original.json")
	command := exec.CommandContext(ctx, filepath.Join(root, "../ponder-upstream/packages/core/node_modules/.bin/vitest"), "run", "--config", "tools/config-live.config.mjs", "--configLoader", "native", "src/build/config.test.ts", "--reporter=json", "--outputFile="+oracle)
	command.Dir = root
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DATABASE_URL=") {
			env = append(env, entry)
		}
	}
	command.Env = append(env, "CI=1", "PONDER_CONFIG_NATIVE_ENDPOINT="+server.URL, "PONDER_CONFIG_CAPTURE_OUTPUT="+filepath.Join(output, "calls.json"), "PONDER_BIND_AUDIT="+filepath.Join(output, "bindings.jsonl"), "NODE_OPTIONS=--import="+filepath.Join(root, "tools/loopback-only.mjs"))
	log, err := os.Create(filepath.Join(output, "output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	started := time.Now()
	runErr := command.Run()
	log.Close()
	mu.Lock()
	defer mu.Unlock()

	sourceContext := []map[string]string{}
	for _, file := range []string{"../ponder-upstream/packages/core/src/build/config.test.ts", "../ponder-upstream/packages/core/src/build/config.ts", "../ponder-upstream/packages/core/src/config/index.ts", "../ponder-upstream/packages/core/src/config/address.ts", "../ponder-upstream/packages/core/src/_test/setup.ts", "../ponder-upstream/packages/core/src/_test/globalSetup.ts", "../ponder-upstream/packages/core/src/_test/utils.ts", "../ponder-upstream/packages/core/src/_test/constants.ts", "../ponder-upstream/packages/core/src/_test/generated.ts", "../ponder-upstream/packages/core/src/utils/abi.ts", "../ponder-upstream/packages/core/src/utils/chains.ts", "../ponder-upstream/packages/core/src/runtime/filter.ts", "../ponder-upstream/packages/core/src/rpc/index.ts", "../ponder-upstream/packages/core/src/utils/finality.ts", "../ponder-upstream/pnpm-lock.yaml", "config/source_upstream_test.go", "tools/config-live.setup.ts", "tools/config-live.config.mjs", "tools/full-oracle.config.mjs", "tools/loopback-only.mjs"} {
		content, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		sourceContext = append(sourceContext, map[string]string{"file": file, "sha256": fmt.Sprintf("%x", digest)})
	}
	evidence := map[string]any{"started_utc": started.UTC().Format(time.RFC3339Nano), "elapsed_ms": time.Since(started).Milliseconds(), "calls": calls, "cases": cases, "command": command.Args, "source_type_obligations_satisfied": false, "source_context": sourceContext}
	data, _ := json.MarshalIndent(evidence, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "native.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Log("Live original/native evidence:", output)
	if runErr != nil {
		content, _ := os.ReadFile(filepath.Join(output, "output.txt"))
		t.Fatalf("Original source run failed: %v\n%s", runErr, content)
	}
	raw, err := os.ReadFile(oracle)
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Success                                         bool
		NumPassedTests, NumPendingTests, NumFailedTests int
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Success || status.NumPassedTests != 39 || status.NumPendingTests != 1 || status.NumFailedTests != 0 {
		t.Fatalf("Incomplete original oracle: %+v", status)
	}

	typedControls := 0
	for _, call := range calls {
		for _, control := range call.TypedControls {
			typedControls++
			if control.ExitCode != 0 {
				t.Errorf("Typed positive control failed: %s %s", control.Mode, control.Error)
			}
		}
	}
	if typedControls != 9 {
		t.Fatalf("Expected nine original live typed positive controls, got %d", typedControls)
	}
	hyperliquidChecks := 0
	for _, call := range calls {
		hyperliquidChecks += call.HyperliquidIdentityChecks
	}
	if hyperliquidChecks != 2 {
		t.Fatalf("Original Hyperliquid toBe requires canonical identity checks in config and indexing, got %d", hyperliquidChecks)
	}
	proofs := 0
	for _, call := range calls {
		if call.TypeProof != nil {
			proofs++
		}
	}
	if proofs != 1 {
		t.Fatalf("Expected one actual filter-union positive execution, got %d", proofs)
	}
	if len(cases) != 39 {
		t.Fatalf("Need 39 complete source cases; got %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Title+sourceParameterSuffix(c.Parameter), func(t *testing.T) {
			n := 0
			for _, call := range calls {
				if call.CaseID != c.ID {
					continue
				}
				n++
				for _, difference := range call.Differences {
					t.Errorf("%s: %s (see native.json)", call.Operation, difference)
				}
			}
			if n == 0 {
				t.Fatal("No actual native calls")
			}
		})
	}
}
func sourceParameterSuffix(value any) string {
	if value == nil {
		return ""
	}
	data, _ := json.Marshal(value)
	return "/" + string(bytes.TrimSpace(data))
}
