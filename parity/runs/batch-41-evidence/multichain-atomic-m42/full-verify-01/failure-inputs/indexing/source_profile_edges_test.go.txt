package indexing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"ponder.local/ponder-go/filter"
)

// TestSourceIndexingProfileEdges is supplemental only. It observes independently
// executed record/recover/key operations for the source vector INPUTS. In
// particular, a source hint is matched to an earlier native record; source
// expected results are never used to initialize native hints.
func TestSourceIndexingProfileEdges(t *testing.T) {
	raw, fixture := readSourceProfileFixture(t, "../testdata/indexing/source-indexing-profile-probes.json.gz")
	casesByBackend := indexingObject(fixture["backend_cases"])
	observations := []Object{}
	for _, backend := range []string{"postgres", "pglite"} {
		t.Run(backend, func(t *testing.T) {
			var vectors []Object
			for _, c := range indexingObjects(casesByBackend[backend]) {
				if c["name"] == "profile-edges" {
					vectors = indexingObjects(c["vectors"])
				}
			}
			if len(vectors) != 21 {
				t.Fatalf("source profile edge census %d != 21", len(vectors))
			}
			records := []*RecordedProfile{}
			actual := []Object{}
			allPass := true
			for _, vector := range vectors {
				passed := t.Run(vector["name"].(string), func(t *testing.T) {
					event := indexingObject(indexingUnmark(t, vector["event"]))
					arg := indexingObject(indexingUnmark(t, vector["args"]))
					call := profileSourceCall(t, arg)
					cache, _ := arg["cache"].(string)
					hints := []*RecordedProfile{}
					for _, input := range indexingObjects(indexingUnmark(t, vector["hints"])) {
						var hint *RecordedProfile
						wanted, _ := json.Marshal(indexingCanonical(input))
						for _, candidate := range records {
							encoded, _ := json.Marshal(indexingCanonical(profileObjectRecord(candidate)))
							if string(encoded) == string(wanted) {
								hint = candidate
								break
							}
						}
						if hint == nil {
							t.Fatal("source hint has no prior independently computed native record")
						}
						hints = append(hints, hint)
					}
					result, err := RecordProfilePattern(event, call, cache, hints, profileSourceFieldOrder(vector["field_order"]))
					if err != nil {
						t.Fatal(err)
					}
					indexingEqual(t, "actual source record", profileObjectRecord(result), indexingUnmark(t, vector["result"]))
					hintIndex := -1
					for i, hint := range hints {
						if result == hint {
							hintIndex = i
							break
						}
					}
					if hintIndex != int(vector["result_hint_index"].(float64)) {
						t.Fatal("actual hint identity differs")
					}
					observation := Object{"name": vector["name"], "result": profileObjectRecord(result), "result_hint_index": hintIndex}
					if result != nil {
						records = append(records, result)
						recovered, err := RecoverProfilePattern(result.Pattern, event)
						if err != nil {
							t.Fatal(err)
						}
						indexingEqual(t, "actual source recovery", profileObjectRequest(recovered), indexingUnmark(t, vector["recovered"]))
						key, err := GetProfilePatternKey(result.Pattern)
						if err != nil {
							t.Fatal(err)
						}
						indexingEqual(t, "exact source profile key", key, vector["key"])
						observation["recovered"] = profileObjectRequest(recovered)
						observation["key"] = key
					} else if _, ok := vector["recovered"]; ok {
						t.Fatal("undefined record unexpectedly has recovery")
					}
					actual = append(actual, observation)
				})
				allPass = allPass && passed
			}
			digest := indexingCanonical(actual)
			encoded, err := json.Marshal(digest)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(encoded)
			observations = append(observations, Object{"backend": backend, "id": "profile-edges", "go_test": "TestSourceIndexingProfileEdges", "count": len(actual), "pass": allPass, "digest_input": digest, "observation_sha256": hex.EncodeToString(hash[:])})
		})
	}
	appendSourceProfileArtifact(t, "PONDER_INDEXING_PROFILE_PROBES_NATIVE_OUTPUT", raw, observations)
}

func TestProfilePrimitiveEquality(t *testing.T) {
	for _, pair := range []struct {
		a, b  any
		equal bool
	}{
		{0, "0", false}, {false, "false", false}, {1, "1", true}, {true, "true", true},
		{filter.Undefined, filter.Undefined, true}, {filter.Undefined, nil, false},
		{[]any{1}, "1", true}, {Object{}, "[object Object]", true},
	} {
		if actual := profileEqual(pair.a, pair.b); actual != pair.equal {
			t.Errorf("eq(%#v,%#v)=%v, want %v", pair.a, pair.b, actual, pair.equal)
		}
	}
}
