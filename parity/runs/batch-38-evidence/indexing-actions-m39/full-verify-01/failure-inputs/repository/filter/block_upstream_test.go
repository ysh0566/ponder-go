package filter

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestUpstreamBlockFilter(t *testing.T) {
	raw, err := os.ReadFile("../testdata/runtime/block-filter.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Initial  Object `json:"initial_filter"`
		Expected []bool
		Mutation Object
		RPC      struct {
			Method string
			Params []any
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	client := sourceAnvilFixture(t)
	var block Object
	if err = client.Call(context.Background(), fixture.RPC.Method, fixture.RPC.Params, &block); err != nil {
		t.Fatal(err)
	}
	if block["number"] != "0x0" {
		t.Fatal("source fixture must use actual genesis", block)
	}
	filter := FixtureValue(fixture.Initial).(map[string]any)
	if got := IsBlockFilterMatched(filter, block); got != fixture.Expected[0] {
		t.Fatal("first original assertion", got)
	}
	for key, value := range fixture.Mutation {
		filter[key] = value
	}
	if got := IsBlockFilterMatched(filter, block); got != fixture.Expected[1] {
		t.Fatal("second original assertion", got)
	}
}
