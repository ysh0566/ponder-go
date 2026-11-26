//go:build simulation

package runtime

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

const depositTopic = "0xe1fffcc4923d04b559f4d29a8bfc6cda04eb5b0d3c460751c2402c5c5cc9109c"

type depositArchive struct {
	Blocks []Block
	Logs   []Log
}

// This archive projects the exact address/topic query and the block fields used
// by this application. Reject broader queries rather than returning partial data.
func (a depositArchive) transport(chainID uint64, address string) rpc.TransportFunc {
	byNumber := map[string]Block{}
	byHash := map[string][]Log{}
	for _, block := range a.Blocks {
		byNumber[block.Number] = block
		byHash[block.Hash] = []Log{}
	}
	for _, log := range a.Logs {
		byHash[log.BlockHash] = append(byHash[log.BlockHash], log)
	}
	byNumber["latest"] = a.Blocks[len(a.Blocks)-1]
	return func(_ context.Context, method string, params any) (json.RawMessage, error) {
		switch method {
		case "eth_chainId":
			return json.Marshal(Hex(chainID))
		case "eth_getBlockByNumber":
			args, ok := params.([]any)
			if ok && len(args) == 2 {
				block, found := byNumber[fmt.Sprint(args[0])]
				if found {
					return json.Marshal(block)
				}
			}
		case "eth_getLogs":
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var filters []struct {
				BlockHash string
				Address   []string
				Topics    [][]string
			}
			if err := json.Unmarshal(raw, &filters); err != nil {
				return nil, err
			}
			if len(filters) == 1 {
				f := filters[0]
				logs, found := byHash[f.BlockHash]
				if found && len(f.Address) == 1 && strings.EqualFold(f.Address[0], address) &&
					len(f.Topics) == 1 && len(f.Topics[0]) == 1 && f.Topics[0][0] == depositTopic {
					return json.Marshal(logs)
				}
			}
		}
		return nil, fmt.Errorf("request outside captured WETH range and filter: chain %d %s %v", chainID, method, params)
	}
}

func TestSimulationFeatureMultichain(t *testing.T) {
	root := filepath.Join("..", "parity", "simulation", "feature-multichain")
	configData, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	input, err := config.ParseSourceConfiguration(configData)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(root, "archive.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	var archives map[string]depositArchive
	if err := json.NewDecoder(gz).Decode(&archives); err != nil {
		t.Fatal(err)
	}
	opts := config.SourceBuildOptions{RPCTransports: map[string]rpc.TransportFunc{}}
	networks := []struct {
		name    string
		id      uint64
		address string
		count   int
	}{
		{"mainnet", 1, "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", 3582},
		{"optimism", 10, "0x4200000000000000000000000000000000000006", 234},
		{"base", 8453, "0x4200000000000000000000000000000000000006", 7940},
	}
	for _, network := range networks {
		archive := archives[network.name]
		if len(archive.Blocks) != 251 || len(archive.Logs) != network.count {
			t.Fatalf("%s: got %d blocks and %d deposits", network.name, len(archive.Blocks), len(archive.Logs))
		}
		opts.RPCTransports[network.name] = archive.transport(network.id, network.address)
	}
	base, err := config.BuildSourceConfig(input, opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var accounts []struct{ Address, Balance string }
	if err := json.Unmarshal(raw, &accounts); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string, len(accounts))
	for _, account := range accounts {
		want[account.Address] = account.Balance
	}
	if len(want) != 332 || len(accounts) != len(want) {
		t.Fatalf("reference accounts: %d distinct/%d rows", len(want), len(accounts))
	}
	for _, dialect := range []string{"postgres", "pglite"} {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			if dialect == "postgres" && os.Getenv("PONDER_TEST_POSTGRES") == "" {
				t.Fatal("set PONDER_TEST_POSTGRES to a disposable PostgreSQL database")
			}
			backend := stagedBackend(t, context.Background(), dialect, "memory://")
			t.Cleanup(func() { _ = backend.Close() })
			model := schema.Schema{Tables: []schema.Table{{Name: "account", Columns: []schema.Column{{Name: "address", Type: "hex", Primary: true}, {Name: "balance", Type: "bigint", NotNull: true}}}}}
			ns := fmt.Sprintf("ponder_go_sim_weth_%x", time.Now().UnixNano())
			db, err := store.New(ctx, backend, model, ns, schema.Omnichain)
			if err != nil {
				t.Fatal(err)
			}
			if dialect == "postgres" {
				t.Cleanup(func() {
					if err := backend.Exec(context.Background(), `DROP SCHEMA "`+ns+`" CASCADE`); err != nil {
						t.Error(err)
					}
				})
			}
			lastCheckpoint := ""
			counts := map[string]int{}
			deposit := Handler(func(ctx context.Context, c *Context) error {
				if c.Event.ID <= lastCheckpoint {
					return fmt.Errorf("deposit checkpoint %q did not advance past %q", c.Event.ID, lastCheckpoint)
				}
				lastCheckpoint = c.Event.ID
				args := c.Event.Args.(map[string]any)
				wad := args["wad"].(*big.Int)
				_, err := c.DB.Indexing().InsertOne(ctx, "account", store.Row{"address": args["dst"], "balance": wad},
					store.Conflict{Action: store.ConflictUpdate, Update: func(row store.Row) (store.Row, error) {
						return store.Row{"balance": new(big.Int).Add(row["balance"].(*big.Int), wad)}, nil
					}})
				if err == nil {
					counts[c.Chain.Name]++
				}
				return err
			})
			build, err := config.BuildSourceIndexing(ctx, input, base, []config.SourceIndexingFunction{{Name: "weth9:Deposit", Fn: deposit}})
			if err != nil {
				t.Fatal(err)
			}
			indexer, err := NewIndexerFromSource(build, db, Options{})
			if err != nil {
				t.Fatal(err)
			}
			var ready bool
			if err := indexer.RunIndexing(ctx, func(value bool) { ready = value }); err != nil || !ready {
				t.Fatal("completion", ready, err)
			}
			rows, err := db.Read(ctx, "account")
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, row := range rows.Rows {
				got[fmt.Sprint(row["address"])] = fmt.Sprint(row["balance"])
			}
			for _, net := range networks {
				if counts[net.name] != net.count {
					t.Errorf("%s processed %d deposits, want %d", net.name, counts[net.name], net.count)
				}
			}
			if len(rows.Rows) != len(want) || !maps.Equal(got, want) {
				for addr, balance := range want {
					if got[addr] != balance {
						t.Errorf("account %s: got %q, want %s", addr, got[addr], balance)
						break
					}
				}
				t.Fatalf("got %d account rows, want %d", len(rows.Rows), len(want))
			}
		})
	}
}
