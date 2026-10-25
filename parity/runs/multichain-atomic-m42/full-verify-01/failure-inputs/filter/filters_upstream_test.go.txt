package filter

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"ponder.local/ponder-go/rpc"
)

// The source registers setupCommon/setupAnvil even for these pure cases. Their
// contexts remain in the fixture ledger; native tests use an actual clean local
// chain, with the explicitly reported existing Anvil version, never an oracle
// claim for the pending Foundry1.8.5 or a replacement generated contract ABI.
func sourceAnvilFixture(t *testing.T) *rpc.Client {
	t.Helper()
	if _, e := exec.LookPath("anvil"); e != nil {
		t.Fatal("existing local Anvil required for source setup fixture")
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "anvil", "--silent", "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--chain-id", "1")
	if e = cmd.Start(); e != nil {
		cancel()
		t.Fatal(e)
	}
	t.Cleanup(func() { cancel(); cmd.Wait() })
	c := rpc.New(fmt.Sprintf("http://127.0.0.1:%d", port))
	c.Retries = 0
	c.RequestsPerSecond = 0
	deadline := time.Now().Add(10 * time.Second)
	for {
		var id string
		if c.Call(ctx, "eth_chainId", []any{}, &id) == nil && id == "0x1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native source fixture chain unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return c
}
func TestUpstreamFilters(t *testing.T) {
	raw, e := os.ReadFile("../testdata/runtime/filters.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Name       string `json:"source_name"`
			Calls      []sourceCall
			Assertions []struct {
				sourceAssertion
				Path []string
			}
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			sourceAnvilFixture(t)
			results := make([]any, len(c.Calls))
			for i, call := range c.Calls {
				args := FixtureValue(call.Args).([]any)
				input := object(args[0])
				var result any
				switch call.Operation {
				case "getChildAddress":
					result = GetChildAddress(object(input["log"]), object(input["factory"]))
				case "mergeLogFiltersToBlockRequest":
					filters, factories := []Object{}, []Object{}
					for _, x := range input["logFilters"].([]any) {
						filters = append(filters, object(x))
					}
					for _, x := range input["factories"].([]any) {
						factories = append(factories, object(x))
					}
					result = MergeLogFiltersToBlockRequest(filters, factories)
				default:
					t.Fatal("unimplemented source operation", call.Operation)
				}
				results[i] = result
				actual, _ := json.Marshal(result)
				want, _ := json.Marshal(FixtureValue(call.Result))
				if string(actual) != string(want) {
					t.Fatalf("source operation %d:\ngot %s\nwant %s", i, actual, want)
				}
			}
			for i, a := range c.Assertions {
				actual := results[a.CallIndex]
				for _, key := range a.Path {
					actual = get(object(actual), key)
				}
				if a.Matcher == "toHaveLength" {
					xs, ok := arr(actual)
					if !ok || float64(len(xs)) != num(a.Expected) {
						t.Fatal("source length assertion", i, actual, a.Expected)
					}
					continue
				}
				got, _ := json.Marshal(actual)
				want, _ := json.Marshal(FixtureValue(a.Expected))
				if string(got) != string(want) {
					t.Fatalf("source assertion %d %s:\ngot %s\nwant %s", i, a.Matcher, got, want)
				}
			}
		})
	}
}
