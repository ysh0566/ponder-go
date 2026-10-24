package filter

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Draft only. Exact live source fixtures are captured by the complete original
// file and real Vitest assertions. No synthetic ABI, fake RPC or altered helper.
func TestUpstreamRemainingFilters(t *testing.T) {
	data, err := os.ReadFile("../testdata/runtime/captured-original-filters.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
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
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 9 {
		t.Fatal("expected all nine original declarations")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Title, func(t *testing.T) {
			results := []any{}
			for i, call := range c.Calls {
				args := FixtureValue(call.Args).([]any)
				result, err := differentialCall(call.Operation, args)
				if call.Error != nil || err != nil {
					t.Fatal("unexpected source/native error", call.Error, err)
				}
				actual, e := json.Marshal(marked(result))
				if e != nil {
					t.Fatal(e)
				}
				expected, e := json.Marshal(call.Result)
				if e != nil {
					t.Fatal(e)
				}
				if string(actual) != string(expected) {
					t.Fatalf("source call%d %s\ngot%s\nwant%s", i, call.Operation, actual, expected)
				}
				after, e := json.Marshal(marked(args))
				if e != nil {
					t.Fatal(e)
				}
				expectedAfter, e := json.Marshal(call.ArgsAfter)
				if e != nil {
					t.Fatal(e)
				}
				if string(after) != string(expectedAfter) {
					t.Fatal("source input mutation contract", i, string(after), string(expectedAfter))
				}
				results = append(results, result)
			}
			expectedBools := map[string][]any{
				"isLogFactoryMatched()":                                 {true, true, true, false},
				"isLogFilterMatched()":                                  {true, false},
				"isTransactionFilterMatched()":                          {true, false},
				"isTransactionFilterMatched() with null transaction.to": {true, false},
				"isTransferFilterMatched()":                             {true, false, false, true, false},
				"isTraceFilterMatched()":                                {true, false, false},
			}
			if want, ok := expectedBools[c.Title]; ok {
				if !reflect.DeepEqual(results, want) {
					t.Fatal("every original boolean assertion, including both loop frames", results, want)
				}
				return
			}
			if len(results) != 1 {
				t.Fatal("original merge call", len(results))
			}
			requests := results[0].([]Object)
			switch c.Title {
			case "mergeLogFiltersToRequests merges filters with different topic1 values into one request":
				if len(requests) != 1 {
					t.Fatal("original merged length", len(requests))
				}
				query := object(requests[0]["params"].([]any)[0])
				topics := query["topics"].([]any)
				alice := "0x000000000000000000000000f39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
				bob := "0x00000000000000000000000070997970C51812dc3A010C7d01b50e0d17dc79C8"
				if query["address"] != "0x1111111111111111111111111111111111111111" || !reflect.DeepEqual(topics, []any{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", []any{alice, bob}}) {
					t.Fatal("original partial request", query)
				}
			case "mergeLogFiltersToRequests merges filters with different addresses into one request":
				if len(requests) != 1 {
					t.Fatal("original merged length", len(requests))
				}
				query := object(requests[0]["params"].([]any)[0])
				if !reflect.DeepEqual(query["address"], []any{"0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"}) || !reflect.DeepEqual(query["topics"], []any{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"}) {
					t.Fatal("original partial request", query)
				}
			case "mergeLogFiltersToRequests does not merge disjoint intervals into an overly wide request":
				if len(requests) != 2 {
					t.Fatal("original disjoint length", len(requests))
				}
				found := map[string]bool{}
				for _, request := range requests {
					query := object(request["params"].([]any)[0])
					found[str(query["fromBlock"])+":"+str(query["toBlock"])] = true
				}
				if !found["0x1:0x64"] || !found["0x186a0:0x18704"] {
					t.Fatal("original arrayContaining/objectContaining interval assertions", requests)
				}
			default:
				t.Fatal("unhandled original source assertions", c.Title)
			}
		})
	}
}
