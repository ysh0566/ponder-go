// An explicit local Ethereum + PostgreSQL example. See README before running.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/query"
	indexer "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := run(ctx); e != nil && ctx.Err() == nil {
		log.Fatal(e)
	}
}
func run(ctx context.Context) error {
	url, rpcURL, address := os.Getenv("PONDER_LOCAL_DATABASE_URL"), os.Getenv("PONDER_LOCAL_RPC_URL"), os.Getenv("PONDER_LOCAL_CONTRACT")
	if url == "" || rpcURL == "" || address == "" {
		return fmt.Errorf("set explicit PONDER_LOCAL_DATABASE_URL, PONDER_LOCAL_RPC_URL and PONDER_LOCAL_CONTRACT for a task-owned local database and chain")
	}
	backend, e := store.OpenPostgres(ctx, url)
	if e != nil {
		return e
	}
	defer backend.Close()
	raw, e := os.ReadFile("schema.json")
	if e != nil {
		return e
	}
	var model schema.Schema
	if e = json.Unmarshal(raw, &model); e != nil {
		return e
	}
	s, e := store.New(ctx, backend, model, os.Getenv("PONDER_SHADOW_SCHEMA"), schema.Multichain)
	if e != nil {
		return e
	}
	abi := `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]}]`
	chain := config.Chain{Name: "local", ID: 1, RPC: []string{rpcURL}}
	engine, e := indexer.New(config.Config{Chains: []config.Chain{chain}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(abi), Address: []string{address}, IncludeTransactionReceipts: true}}}, s, map[string]indexer.Handler{"Token:Transfer": func(ctx context.Context, c *indexer.Context) error {
		args := c.Event.Args.(map[string]any)
		value := args["value"].(*big.Int)
		for _, leg := range []struct {
			address string
			sign    int
		}{{args["from"].(string), -1}, {args["to"].(string), 1}} {
			row, e := c.DB.Find(ctx, "account", store.Row{"id": leg.address})
			if e != nil {
				return e
			}
			balance := new(big.Int)
			if row != nil {
				balance.SetString(fmt.Sprint(row["balance"]), 10)
			}
			delta := new(big.Int).Mul(value, big.NewInt(int64(leg.sign)))
			balance.Add(balance, delta)
			if e = c.DB.Write(ctx, "account", store.Row{"id": leg.address, "balance": balance.String()}, store.Upsert); e != nil {
				return e
			}
		}
		return nil
	}})
	if e != nil {
		return e
	}
	server := query.New(s)
	server.ChainNames[1] = "local"
	if os.Getenv("PONDER_COMMAND") == "serve" {
		engine = nil
	}
	app, err := indexer.StartApplication(ctx, engine, server, "127.0.0.1", 42069)
	if err != nil {
		return err
	}
	log.Printf("local example at http://%s; indexing schema %s", app.Addr(), s.Namespace)
	return app.Wait()
}
