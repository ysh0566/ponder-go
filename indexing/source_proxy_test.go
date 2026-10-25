package indexing

import (
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"ponder.local/ponder-go/filter"
)

// TestSourceIndexingServiceProxy replays actual source probe inputs. Supplemental
// only: Go has no JS property descriptors, and Node inspect text is normalized
// from the actual Inspect result rather than asserted as a Go formatting API.
func TestSourceIndexingServiceProxy(t *testing.T) {
	raw, e := os.ReadFile("../testdata/indexing/source-indexing-service-proxy.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Real        bool     `json:"real_source"`
		Initial     any      `json:"initial_underlying"`
		Replacement any      `json:"replacement_underlying"`
		Include     []string `json:"default_include"`
		Steps       []Object `json:"steps"`
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if !fixture.Real || len(fixture.Steps) != 23 {
		t.Fatal("incomplete actual-source probe")
	}
	pattern := NewColumnAccessPattern([]string{"Event"})
	profile := pattern["Event"]
	profile.Count = 123
	profile.Resolved = true
	state := &ErrorState{}
	resets := []string{}
	view, e := NewPropertyView("transaction", pattern, state, func(name string) { resets = append(resets, name); profile.Count = 0 })
	if e != nil {
		t.Fatal(e)
	}
	first := indexingUnmark(t, fixture.Initial).(Object)
	underlying := first
	view.Bind("Event", underlying)
	originalView := view
	indexingEqual(t, "default include", view.Keys(), fixture.Include)
	errorValue := func(err error) any {
		if err == nil {
			return nil
		}
		named, ok := err.(interface{ ErrorName() string })
		if !ok {
			t.Fatalf("unexpected error %T %v", err, err)
		}
		return Object{"name": named.ErrorName(), "message": err.Error(), "meta": []any{}}
	}
	for _, step := range fixture.Steps {
		op := step["operation"].(string)
		reads := []string{}
		view.onAccess = func(eventName, component, key string) {
			if eventName != "Event" || component != "transaction" {
				t.Fatal("wrong access scope")
			}
			reads = append(reads, key)
		}
		resets = []string{}
		var value any
		var err error
		switch op {
		case "get-present", "get-missing":
			value, err = view.Get("gas")
		case "get-null":
			value, err = view.Get("maxFeePerGas")
		case "get-present-undefined":
			value, err = view.Get("maxPriorityFeePerGas")
		case "get-untracked":
			value, err = view.Get("extra")
		case "has-present":
			value = view.Has("gas")
		case "has-missing-included":
			value = view.Has("hash")
		case "has-untracked":
			value = view.Has("extra")
		case "ownKeys":
			value = view.Keys()
		case "objectKeys":
			// No descriptor API exists on the explicit Go facade. Do not substitute
			// Reflect.ownKeys or a fabricated empty result as claimed parity.
			t.Log("source Object.keys descriptor reflection has no Go facade counterpart; supplemental gap")
		case "set-present":
			err = view.Set("gas", big.NewInt(12))
			value = err == nil
		case "delete-present", "delete-missing":
			err = view.Delete("gas")
			value = err == nil
		case "set-missing":
			err = view.Set("gas", big.NewInt(15))
			value = err == nil
		case "set-untracked":
			err = view.Set("newExtra", 99)
			value = err == nil
		case "delete-untracked":
			err = view.Delete("extra")
			value = err == nil
		case "inspect-missing":
			value, err = view.Inspect()
		case "identity-after-underlying-replacement":
			underlying = indexingUnmark(t, fixture.Replacement).(Object)
			view.Bind("Event", underlying)
			var got any
			got, err = view.Get("gas")
			_, present := first["gas"]
			value = Object{"sameProxy": view == originalView, "value": got, "originalMissing": !present, "stickyPreserved": state.GetRetryableError() != nil}
		case "inspect-complete":
			var inspected Object
			inspected, err = view.Inspect()
			if err == nil {
				// Compare the semantic fields parsed from Node's inspect output;
				// do not manufacture that presentation format as a Go result.
				expected := Object{}
				for _, line := range strings.Split(step["value"].(string), "\n") {
					line = strings.TrimSuffix(strings.TrimSpace(line), ",")
					pair := strings.SplitN(line, ": ", 2)
					if len(pair) != 2 {
						continue
					}
					if pair[1] == "null" {
						expected[pair[0]] = nil
					} else if strings.HasSuffix(pair[1], "n") {
						n, ok := new(big.Int).SetString(strings.TrimSuffix(pair[1], "n"), 10)
						if !ok {
							t.Fatal("invalid source inspect bigint")
						}
						expected[pair[0]] = n
					} else {
						t.Fatalf("unsupported source inspect scalar %s", pair[1])
					}
				}
				indexingEqual(t, op+" semantic fields", inspected, expected)
			}
		case "disabled-get-missing":
			view.SetDisabled(true)
			value, err = view.Get("notIncluded")
		case "disabled-set-missing-included":
			err = view.Set("gasPrice", filter.Undefined)
			value = err == nil
		case "disabled-delete-included":
			err = view.Delete("gas")
			value = err == nil
		case "disabled-has-missing-included":
			value = view.Has("gas")
		default:
			t.Fatalf("unhandled actual source operation %q", op)
		}
		indexingEqual(t, op+" reads", reads, step["reads"])
		indexingEqual(t, op+" resets", resets, step["resets"])
		indexingEqual(t, op+" profile", Object{"count": profile.Count, "resolved": profile.Resolved, "transaction": profile.Transaction}, step["profile"])
		indexingEqual(t, op+" sticky", errorValue(state.GetRetryableError()), step["sticky"])
		indexingEqual(t, op+" underlying", underlying, step["underlying"])
		if expected, failed := step["error"]; failed {
			if err == nil {
				t.Fatal(op, "missing error")
			}
			indexingEqual(t, op+" error", errorValue(err), expected)
			if state.GetRetryableError() != err {
				t.Fatal(op, "sticky error identity changed")
			}
		} else {
			if err != nil {
				t.Fatal(op, err)
			}
			if op != "objectKeys" && op != "inspect-complete" {
				indexingEqual(t, op+" value", value, step["value"])
			}
		}
	}
	view.SetDisabled(false)
	if state.GetRetryableError() == nil {
		t.Fatal("sticky error cleared by successful accesses")
	}
}
