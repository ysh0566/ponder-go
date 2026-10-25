package indexing

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	ponderabi "ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/filter"
)

var sourceProfileIDs = []string{"2f670307cc3e54bf3901", "465e8823325559c8cfa1", "e7d2206043a345533ec0", "fcafee958f55fe65dc77", "fb137834371e07d16724", "732ac50bf3a96196de3e", "4c57d8f36ec9b90f69ac", "d8952ac4024fd02802a7", "0173bc5e83b0843a8d4e", "ddf6318af43ddc28cd80"}

func readSourceProfileFixture(t *testing.T, path string) ([]byte, Object) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	var fixture Object
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return raw, fixture
}
func profileSourceCall(t *testing.T, value Object) ContractCall {
	t.Helper()
	call := sourceActionContract(t, value)
	call.ABIValue = value["abi"]
	return call
}
func profileSourceValue(t *testing.T, value any) ProfileValue {
	t.Helper()
	v := indexingObject(value)
	return ProfileValue{Type: v["type"].(string), Value: v["value"]}
}
func profileSourcePattern(t *testing.T, value Object) ProfilePattern {
	t.Helper()
	raw, err := json.Marshal(value["abi"])
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ponderabi.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	pattern := ProfilePattern{Address: profileSourceValue(t, value["address"]), ABI: parsed, ABIValue: value["abi"], FunctionName: value["functionName"].(string)}
	pattern.Cache, _ = value["cache"].(string)
	if args, ok := value["args"].([]any); ok {
		pattern.Args = make([]ProfileValue, len(args))
		for i, v := range args {
			pattern.Args[i] = profileSourceValue(t, v)
		}
	}
	return pattern
}
func profileSourceRecord(t *testing.T, value Object) *RecordedProfile {
	t.Helper()
	return &RecordedProfile{Pattern: profileSourcePattern(t, indexingObject(value["pattern"])), HasConstant: value["hasConstant"].(bool)}
}
func profileObjectValue(value ProfileValue) Object {
	return Object{"type": value.Type, "value": value.Value}
}
func profileObjectPattern(pattern ProfilePattern) Object {
	var args any = filter.Undefined
	if pattern.Args != nil {
		a := make([]any, len(pattern.Args))
		for i, v := range pattern.Args {
			a[i] = profileObjectValue(v)
		}
		args = a
	}
	var cache any = filter.Undefined
	if pattern.Cache != "" {
		cache = pattern.Cache
	}
	return Object{"address": profileObjectValue(pattern.Address), "abi": pattern.ABIValue, "functionName": pattern.FunctionName, "args": args, "cache": cache}
}
func profileObjectRecord(record *RecordedProfile) any {
	if record == nil {
		return filter.Undefined
	}
	return Object{"pattern": profileObjectPattern(record.Pattern), "hasConstant": record.HasConstant}
}
func profileObjectRequest(request ProfileRequest) Object {
	var args any = filter.Undefined
	if request.Args != nil {
		args = request.Args
	}
	return Object{"address": request.Address, "abi": request.ABIValue, "functionName": request.FunctionName, "args": args, "blockNumber": request.BlockNumber, "chainId": request.ChainID}
}
func profileSourceFieldOrder(value any) map[string][]string {
	result := map[string][]string{}
	if obj, ok := value.(Object); ok {
		for name, value := range obj {
			if entries, ok := value.([]any); ok {
				for _, entry := range entries {
					result[name] = append(result[name], entry.(string))
				}
			}
		}
	}
	return result
}

// TestSourceIndexingProfile executes each unchanged source record/recover input.
// Outputs and inline snapshots only assert independently computed native values;
// hints reference earlier ACTUAL records, including source pointer identities.
func TestSourceIndexingProfile(t *testing.T) {
	raw, fixture := readSourceProfileFixture(t, "../testdata/indexing/source-indexing-profile.json.gz")
	casesByBackend := indexingObject(fixture["backend_cases"])
	observations := []Object{}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			cases := indexingObjects(casesByBackend[backend])
			if len(cases) != 24 {
				t.Fatalf("source declaration census %d != 24", len(cases))
			}
			selected := map[string]bool{}
			for _, id := range sourceProfileIDs {
				selected[id] = false
			}
			for _, record := range cases {
				id, _ := record["id"].(string)
				if _, ok := selected[id]; !ok {
					continue
				}
				if selected[id] {
					t.Fatal("duplicate source profile", id)
				}
				selected[id] = true
				var observation Object
				pass := t.Run(id, func(t *testing.T) { observation = replaySourceProfile(t, backend, record) })
				if observation != nil {
					observation["pass"] = pass
					observations = append(observations, observation)
				}
			}
			for id, seen := range selected {
				if !seen {
					t.Fatal("missing source profile", id)
				}
			}
		})
	}
	if len(observations) != 20 {
		t.Fatalf("requires all 20 profile backend cases, got %d", len(observations))
	}
	appendSourceProfileArtifact(t, "PONDER_INDEXING_PROFILE_NATIVE_OUTPUT", raw, observations)
}
func replaySourceProfile(t *testing.T, backend string, record Object) Object {
	t.Helper()
	ops := indexingObjects(record["profile_ops"])
	actualRecords := map[int]*RecordedProfile{}
	actualOps := []Object{}
	for opIndex, op := range ops {
		event := indexingObject(indexingUnmark(t, op["event"]))
		switch op["operation"] {
		case "recordProfilePattern":
			arg := indexingObject(indexingUnmark(t, op["args"]))
			call := profileSourceCall(t, arg)
			cache, _ := arg["cache"].(string)
			hints := []*RecordedProfile{}
			sourceHints := indexingObjects(indexingUnmark(t, op["hints"]))
			indices := op["hint_indexes"].([]any)
			if len(indices) != len(sourceHints) {
				t.Fatal("hint identity census")
			}
			for i, sourceHint := range sourceHints {
				index := int(indices[i].(float64))
				var hint *RecordedProfile
				if index >= 0 {
					var ok bool
					hint, ok = actualRecords[index]
					if !ok {
						t.Fatal("hint references missing native record", index)
					}
				} else {
					hint = profileSourceRecord(t, sourceHint)
				}
				indexingEqual(t, "hint input", profileObjectRecord(hint), sourceHint)
				hints = append(hints, hint)
			}
			result, err := RecordProfilePattern(event, call, cache, hints, profileSourceFieldOrder(op["field_order"]))
			if err != nil {
				t.Fatal(err)
			}
			actualRecords[opIndex] = result
			indexingEqual(t, "record output", profileObjectRecord(result), indexingUnmark(t, op["result"]))
			hintIndex := -1
			for i, hint := range hints {
				if result == hint {
					hintIndex = i
					break
				}
			}
			if hintIndex != int(op["result_hint_index"].(float64)) {
				t.Fatal("actual hint identity mismatch")
			}
			actualOps = append(actualOps, Object{"operation": "recordProfilePattern", "result": profileObjectRecord(result), "result_hint_index": hintIndex})
		case "recoverProfilePattern":
			index := int(op["pattern_record_index"].(float64))
			var pattern ProfilePattern
			if index >= 0 {
				record, ok := actualRecords[index]
				if !ok || record == nil {
					t.Fatal("recover references missing actual record", index)
				}
				pattern = record.Pattern
				indexingEqual(t, "recover input pattern", profileObjectPattern(pattern), indexingUnmark(t, op["pattern"]))
			} else {
				pattern = profileSourcePattern(t, indexingObject(indexingUnmark(t, op["pattern"])))
			}
			result, err := RecoverProfilePattern(pattern, event)
			if err != nil {
				t.Fatal(err)
			}
			indexingEqual(t, "recovered request", profileObjectRequest(result), indexingUnmark(t, op["result"]))
			actualOps = append(actualOps, Object{"operation": "recoverProfilePattern", "result": profileObjectRequest(result)})
		default:
			t.Fatal("unexpected profile operation", op["operation"])
		}
	}
	id := record["id"].(string)
	checked := 2
	if id == "0173bc5e83b0843a8d4e" {
		checked = 3
		if actualRecords[0] == actualRecords[1] {
			t.Fatal("original not.toBe(hint)")
		}
		indexingEqual(t, "original args path assertion", profileObjectValue(actualRecords[1].Pattern.Args[0]), Object{"type": "derived", "value": []string{"args", "to"}})
	}
	if len(record["assertions"].([]any)) != checked {
		t.Fatal("original assertion census changed")
	}
	digest := indexingCanonical(Object{"operations": actualOps})
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	return Object{"backend": backend, "id": id, "name": record["full_name"], "source_assertions_checked": checked, "digest_input": digest, "observation_sha256": hex.EncodeToString(hash[:])}
}

var sourceProfileArtifactMutex sync.Mutex

// Each root may run in a separate serial verification batch. Preserve the other
// root's observations on disk; never fabricate missing records or accept a
// different fixture. The orchestrator owns fresh per-run output paths.
func appendSourceProfileArtifact(t *testing.T, envName string, fixtureRaw []byte, observations []Object) {
	t.Helper()
	output := os.Getenv(envName)
	if output == "" {
		return
	}
	sourceProfileArtifactMutex.Lock()
	defer sourceProfileArtifactMutex.Unlock()
	sum := sha256.Sum256(fixtureRaw)
	hash := hex.EncodeToString(sum[:])
	cases := []Object{}
	if raw, err := os.ReadFile(output); err == nil {
		var prior struct {
			Hash  string   `json:"source_fixture_sha256"`
			Cases []Object `json:"cases"`
		}
		if err = json.Unmarshal(raw, &prior); err != nil {
			t.Fatal(err)
		}
		if prior.Hash != hash {
			t.Fatal("native artifact existing fixture mismatch")
		}
		cases = prior.Cases
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	byKey := map[string]int{}
	key := func(v Object) string { return fmt.Sprint(v["backend"]) + "/" + fmt.Sprint(v["id"]) }
	for i, v := range cases {
		k := key(v)
		if _, ok := byKey[k]; ok {
			t.Fatal("duplicate existing artifact case", k)
		}
		byKey[k] = i
	}
	for _, v := range observations {
		k := key(v)
		if i, ok := byKey[k]; ok {
			cases[i] = v
		} else {
			byKey[k] = len(cases)
			cases = append(cases, v)
		}
	}
	sort.Slice(cases, func(i, j int) bool { return key(cases[i]) < key(cases[j]) })
	artifact := Object{"source_credit": 0, "source_fixture_sha256": hash, "cases": cases}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".profile-native-*")
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(name, output); err != nil {
		t.Fatal(err)
	}
}
