//go:build simulation

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func simulationERC20Event(ctx context.Context, c *Context) error {
	args := c.Event.Args.(map[string]any)
	amount := args["amount"].(*big.Int)
	timestamp, err := Number(c.Event.Block.Timestamp)
	if err != nil {
		return err
	}
	row := store.Row{"id": c.Event.ID, "timestamp": timestamp, "amount": amount}
	switch c.Event.Name {
	case "ERC20:Transfer":
		for _, side := range []string{"from", "to"} {
			address := args[side].(string)
			delta := new(big.Int).Set(amount)
			if side == "from" {
				delta.Neg(delta)
			}
			// The simulation starts every new account at zero, including the recipient.
			_, err := c.DB.Indexing().InsertOne(ctx, "account", store.Row{"address": address, "balance": new(big.Int), "isOwner": false},
				store.Conflict{Action: store.ConflictUpdate, Update: func(old store.Row) (store.Row, error) {
					return store.Row{"balance": new(big.Int).Add(old["balance"].(*big.Int), delta)}, nil
				}})
			if err != nil {
				return err
			}
			row[side] = address
		}
		return c.DB.Write(ctx, "transfer_event", row, store.Insert)
	case "ERC20:Approval":
		allowance := store.Row{"owner": args["owner"], "spender": args["spender"], "amount": amount}
		if err := c.DB.Write(ctx, "allowance", allowance, store.Upsert); err != nil {
			return err
		}
		row["owner"], row["spender"] = args["owner"], args["spender"]
		return c.DB.Write(ctx, "approval_event", row, store.Insert)
	default:
		return fmt.Errorf("unexpected ERC-20 event %q", c.Event.Name)
	}
}

func TestSimulationReferenceERC20(t *testing.T) {
	root := filepath.Join("..", "parity", "simulation", "reference-erc20")
	raw, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	input, err := config.ParseSourceConfiguration(raw)
	if err != nil {
		t.Fatal(err)
	}
	var chains map[string]simulationArchive
	readSimulationGzip(t, filepath.Join(root, "archive.json.gz"), &chains)
	archive := chains["mainnet"]
	if len(archive.Blocks) != 2001 || len(archive.Logs) != 4970 {
		t.Fatalf("unexpected archive: %d blocks and %d logs", len(archive.Blocks), len(archive.Logs))
	}
	transport := archive.transport(1, "0x32353A6C91143bfd6C7d363B546e62a9A2489A20",
		"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef",
		"0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925")
	errorsFile, err := os.ReadFile(filepath.Join(root, "initial-block-errors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var failures map[string]int
	if err := json.Unmarshal(errorsFile, &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures) != 39 {
		t.Fatalf("expected seeded errors at 39 heights, got %d", len(failures))
	}
	var tables map[string][]map[string]any
	readSimulationGzip(t, filepath.Join(root, "tables.json.gz"), &tables)
	raw, err = os.ReadFile(filepath.Join("..", "cmd", "ponder", "scaffold", "api-functions", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var model schema.Schema
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 4 || len(tables["transfer_event"]) != 3757 || len(tables["approval_event"]) != 1213 {
		t.Fatal("incomplete reference tables")
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			db := simulationStore(t, dialect, model, schema.Omnichain)
			remaining := maps.Clone(failures)
			var mu sync.Mutex
			withFailures := func(ctx context.Context, method string, params any) (json.RawMessage, error) {
				if args, ok := params.([]any); method == "eth_getBlockByNumber" && ok && len(args) == 2 && args[1] == true {
					height, _ := args[0].(string)
					mu.Lock()
					count := remaining[height]
					if count == 1 {
						delete(remaining, height)
					} else if count > 1 {
						remaining[height] = count - 1
					}
					mu.Unlock()
					if count > 0 {
						return nil, fmt.Errorf("simulated archive read failure at %s", height)
					}
				}
				return transport(ctx, method, params)
			}
			base, err := config.BuildSourceConfig(input, config.SourceBuildOptions{RPCTransports: map[string]rpc.TransportFunc{"mainnet": withFailures}})
			if err != nil {
				t.Fatal(err)
			}
			last := ""
			handler := Handler(func(ctx context.Context, c *Context) error {
				if c.Event.ID <= last {
					return fmt.Errorf("ERC-20 checkpoint %q did not follow %q", c.Event.ID, last)
				}
				last = c.Event.ID
				return simulationERC20Event(ctx, c)
			})
			index, err := config.BuildSourceIndexing(ctx, input, base, []config.SourceIndexingFunction{{Name: "ERC20:Transfer", Fn: handler}, {Name: "ERC20:Approval", Fn: handler}})
			if err != nil {
				t.Fatal(err)
			}
			indexer, err := NewIndexerFromSource(index, db, Options{})
			if err != nil {
				t.Fatal(err)
			}
			var ready bool
			if err := indexer.RunIndexing(ctx, func(value bool) { ready = value }); err != nil || !ready {
				t.Fatal("bounded completion", ready, err)
			}
			if len(remaining) != 0 {
				t.Fatalf("%d scheduled header failures did not execute", len(remaining))
			}
			for _, name := range []string{"account", "allowance", "transfer_event", "approval_event"} {
				t.Run(name, func(t *testing.T) {
					got, err := db.Read(ctx, name)
					if err != nil {
						t.Fatal(err)
					}
					compareSimulationRows(t, name, got.Rows, tables[name])
				})
			}
		})
	}
}

func compareSimulationRows(t *testing.T, table string, got, want []map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s has %d rows, want %d", table, len(got), len(want))
	}
	key := func(row map[string]any) string {
		switch table {
		case "account":
			return fmt.Sprint(row["address"])
		case "allowance":
			return fmt.Sprint(row["owner"], "/", row["spender"])
		default:
			return fmt.Sprint(row["id"])
		}
	}
	rows := make(map[string]string, len(want))
	for _, row := range want {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		rows[key(row)] = string(data)
	}
	if len(rows) != len(want) {
		t.Fatal("duplicate reference primary key")
	}
	for _, row := range got {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		pk := key(row)
		if rows[pk] != string(data) {
			t.Fatalf("%s %s: got %s, want %s", table, pk, data, rows[pk])
		}
		delete(rows, pk)
	}
}
