package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ponder.local/ponder-go/abi"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/internal/testanvil"
	"ponder.local/ponder-go/query"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
)

func applicationABI(t *testing.T, core, name string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(core, "src", "_test", "contracts", "out", name+".sol", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		ABI json.RawMessage `json:"abi"`
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	return artifact.ABI
}

func mintAccounts(ctx context.Context, c *Context) error {
	args := c.Event.Args.(map[string]any)
	amount := args["amount"].(*big.Int)
	db := c.DB.Indexing()
	for _, leg := range []struct {
		address string
		delta   *big.Int
	}{
		{args["from"].(string), new(big.Int).Neg(amount)}, {args["to"].(string), amount},
	} {
		change := func(row store.Row) (store.Row, error) {
			balance, ok := new(big.Int).SetString(fmt.Sprint(row["balance"]), 10)
			if !ok {
				return nil, fmt.Errorf("invalid account balance %v", row["balance"])
			}
			return store.Row{"balance": balance.Add(balance, leg.delta)}, nil
		}
		if _, err := db.InsertOne(ctx, "account", store.Row{"address": strings.ToLower(leg.address), "balance": leg.delta}, store.Conflict{Action: store.ConflictUpdate, Update: change}); err != nil {
			return err
		}
	}
	return nil
}

func insertSwap(ctx context.Context, c *Context) error {
	args := c.Event.Args.(map[string]any)
	if c.Event.ID == "" {
		return fmt.Errorf("swap has no event ID")
	}
	_, err := c.DB.Indexing().InsertOne(ctx, "swap_event", store.Row{"id": c.Event.ID, "pair": c.Event.Log.Address, "from": strings.ToLower(args["sender"].(string)), "to": strings.ToLower(args["to"].(string))}, store.Conflict{})
	return err
}

func TestSourceApplications(t *testing.T) {
	upstream := os.Getenv("PONDER_UPSTREAM")
	if upstream == "" {
		upstream = "../../ponder-upstream"
	}
	core, err := filepath.Abs(filepath.Join(upstream, "packages", "core"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/application.cjs")
	if err != nil {
		t.Fatal(err)
	}
	const address = "0x5fbdb2315678afecb367f032d93f642f64180aa3"
	for _, kind := range []string{"erc20", "factory"} {
		t.Run(kind, func(t *testing.T) {
			for _, dialect := range []string{"postgres", "pglite"} {
				t.Run(dialect, func(t *testing.T) {
					ctx := t.Context()
					endpoint := testanvil.Start(t, "--no-mining")
					backend := stagedBackend(t, ctx, dialect, "memory://")
					t.Cleanup(func() { backend.Close() })
					table := schema.Table{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}
					contract := map[string]any{"chain": "mainnet", "address": address}
					name := "Erc20"
					callback := config.SourceIndexingFunction{Name: "Erc20:Transfer(address indexed from, address indexed to, uint256 amount)", Fn: Handler(mintAccounts)}
					if kind == "erc20" {
						contract["abi"] = applicationABI(t, core, "ERC20")
					} else {
						name = "Pair"
						table = schema.Table{Name: "swap_event", Columns: []schema.Column{{Name: "id", Type: "text", Primary: true}, {Name: "pair", Type: "hex", NotNull: true}, {Name: "from", Type: "hex", NotNull: true}, {Name: "to", Type: "hex", NotNull: true}}}
						contract["abi"] = applicationABI(t, core, "Pair")
						parent, err := abi.Parse(string(applicationABI(t, core, "Factory")))
						if err != nil {
							t.Fatal(err)
						}
						created, ok := parent.FindEventSafe("PairCreated")
						if !ok {
							t.Fatal("factory ABI missing PairCreated")
						}
						contract["address"] = map[string]any{"address": address, "event": created, "parameter": "pair"}
						callback = config.SourceIndexingFunction{Name: "Pair:Swap", Fn: Handler(insertSwap)}
					}
					db, err := store.New(ctx, backend, schema.Schema{Tables: []schema.Table{table}}, fmt.Sprintf("ponder_go_app_%x", time.Now().UnixNano()), schema.Multichain)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.Marshal(map[string]any{"chains": map[string]any{"mainnet": map[string]any{"id": 1, "rpc": endpoint, "pollingInterval": 100}}, "contracts": map[string]any{name: contract}})
					if err != nil {
						t.Fatal(err)
					}
					project, err := config.ParseSourceConfiguration(raw)
					if err != nil {
						t.Fatal(err)
					}
					built, err := config.BuildSourceConfig(project, config.SourceBuildOptions{})
					if err != nil {
						t.Fatal(err)
					}
					index, err := config.BuildSourceIndexing(ctx, project, built, []config.SourceIndexingFunction{callback})
					if err != nil {
						t.Fatal(err)
					}
					engine, err := NewFromSource(index, "mainnet", db, Options{})
					if err != nil {
						t.Fatal(err)
					}
					routes := query.New(db)
					routes.ChainNames[1] = "mainnet"
					app, err := StartApplication(ctx, engine, routes, "127.0.0.1", 0)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if err := app.Shutdown(shutdown); err != nil {
							t.Error(err)
						}
					}()
					driver, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					command := exec.CommandContext(driver, "node", script, kind, core, endpoint, "http://"+app.Addr().String())
					output, err := command.CombinedOutput()
					if err != nil {
						t.Fatalf("application driver: %v\n%s", err, output)
					}
				})
			}
		})
	}
}
