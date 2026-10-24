package events

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"testing"

	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/filter"
)

// Replay complete original declarations and exact generated-ABI inputs.
// The no-output case additionally requires the public callback-union type proof.
func TestUpstreamContractEventConstruction(t *testing.T) {
	input, err := os.ReadFile("../testdata/runtime/captured-original-events.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtureData struct {
		Cases []struct {
			ID, Title string
			Calls     []struct {
				Operation string
				Args      []any
				ArgsAfter []any `json:"args_after"`
				Result    any
				Error     *struct{ Name, Message string }
			}
		}
	}
	if err := json.Unmarshal(input, &fixtureData); err != nil {
		t.Fatal(err)
	}
	if len(fixtureData.Cases) != 6 {
		t.Fatal("expected all six exact original declarations")
	}
	for _, c := range fixtureData.Cases {
		t.Run(c.Title, func(t *testing.T) {
			var decoded []Object
			builds := [][]RawEvent{}
			for index, call := range c.Calls {
				args := fixture(call.Args).([]any)
				var actual any
				var err error
				switch call.Operation {
				case "decodeEvents":
					raw := []RawEvent{}
					for _, r := range objects(args[3]) {
						raw = append(raw, rawObject(r))
					}
					decoder := Decoder{}
					decoded, err = decoder.DecodeEvents(object(args[1]), objects(args[2]), raw)
					actual = decoded
				case "buildEvents":
					built, buildErr := BuildEvents(batch(object(args[0])))
					err = buildErr
					actual = built
					builds = append(builds, built)
				default:
					t.Fatal("unknown source operation", call.Operation)
				}
				if call.Error != nil {
					t.Fatal("unexpected source exception", call.Error)
				}
				if err != nil {
					t.Fatal(err)
				}
				result, err := json.Marshal(marked(actual))
				if err != nil {
					t.Fatal(err)
				}
				expected, err := json.Marshal(call.Result)
				if err != nil {
					t.Fatal(err)
				}
				if string(result) != string(expected) {
					t.Fatalf("source call%d %s mismatch\ngot%s\nwant%s", index, call.Operation, result, expected)
				}
				after, err := json.Marshal(marked(args))
				if err != nil {
					t.Fatal(err)
				}
				expectedAfter, err := json.Marshal(call.ArgsAfter)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != string(expectedAfter) {
					t.Fatal("source input mutation contract", index, string(after), string(expectedAfter))
				}
			}
			switch c.Title {
			case "decodeEvents() log":
				if len(decoded) != 1 {
					t.Fatal("original length", len(decoded))
				}
				args := object(object(decoded[0]["event"])["args"])
				amount, ok := args["amount"].(*big.Int)
				if args["from"] != "0x0000000000000000000000000000000000000000" || args["to"] != "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266" || !ok || amount.String() != "1000000000000000000" {
					t.Fatal("original partial args match", args)
				}
			case "decodeEvents() log error", "decodeEvents() trace error":
				if len(decoded) != 0 {
					t.Fatal("original empty length", decoded)
				}
			case "decodeEvents() trace", "decodeEvents() trace w/o output":
				if len(decoded) != 1 {
					t.Fatal("original length", len(decoded))
				}
				event := object(decoded[0]["event"])
				expected := []any{"0x70997970C51812dc3A010C7d01b50e0d17dc79C8", new(big.Int).SetUint64(1000000000000000000)}
				if !reflect.DeepEqual(event["args"], expected) {
					t.Fatal("original strict args", event["args"])
				}
				if c.Title == "decodeEvents() trace" {
					if event["result"] != true {
						t.Fatal("original true result", event["result"])
					}
				} else if !filter.IsUndefined(event["result"]) {
					t.Fatal("original undefined result", event["result"])
				}
			case "buildEvents() trace index ignores traces that match no filter":
				if len(builds) != 2 {
					t.Fatal("original exact/superset actions", len(builds))
				}
				addresses := []any{}
				for _, event := range builds[0] {
					addresses = append(addresses, event.Trace["traceAddress"])
				}
				if !reflect.DeepEqual(addresses, []any{[]any{}, []any{float64(3)}, []any{}}) {
					t.Fatal("original exact trace paths", addresses)
				}
				exact, superset := []string{}, []string{}
				indexes := []string{}
				for _, event := range builds[0] {
					exact = append(exact, event.Checkpoint)
					cp, err := checkpoint.Decode(event.Checkpoint)
					if err != nil {
						t.Fatal(err)
					}
					indexes = append(indexes, fmt.Sprint(cp.EventIndex))
				}
				for _, event := range builds[1] {
					superset = append(superset, event.Checkpoint)
				}
				if !reflect.DeepEqual(exact, superset) {
					t.Fatal("original full75digit checkpoints", exact, superset)
				}
				if !reflect.DeepEqual(indexes, []string{"0", "1", "0"}) {
					t.Fatal("original per-transaction indexes", indexes)
				}
			default:
				t.Fatal("unhandled original assertions", c.Title)
			}
		})
	}
}
