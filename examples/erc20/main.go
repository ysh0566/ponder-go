// An explicit local Ethereum + PostgreSQL example. See README before running.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/query"
	indexer "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
	"log"
	"math/big"
	"os"
	"os/signal"
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
	url := os.Getenv("PONDER_LOCAL_DATABASE_URL")
	if url == "" {
		return fmt.Errorf("set PONDER_LOCAL_DATABASE_URL")
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
	namespace := os.Getenv("PONDER_SHADOW_SCHEMA")
	if os.Getenv("PONDER_COMMAND") == "serve" {
		s, err := store.OpenExisting(ctx, backend, model, namespace, schema.Multichain)
		if err != nil {
			return err
		}
		server, err := query.NewAtNamespace(ctx, s, s.Namespace)
		if err != nil {
			return err
		}
		defer server.Close()
		return serve(ctx, nil, server)
	}
	rpcURL, address := os.Getenv("PONDER_LOCAL_RPC_URL"), os.Getenv("PONDER_LOCAL_CONTRACT")
	if rpcURL == "" || address == "" {
		return fmt.Errorf("set PONDER_LOCAL_RPC_URL and PONDER_LOCAL_CONTRACT to index events")
	}
	s, e := store.New(ctx, backend, model, namespace, schema.Multichain)
	if e != nil {
		return e
	}
	if e = s.EnableQueryNotifications(ctx); e != nil {
		return e
	}
	abi := `[{"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]}]`
	chain := config.Chain{Name: "local", ID: 1, RPC: []string{rpcURL}}
	worker, e := indexer.NewIndexer(config.Config{Chains: []config.Chain{chain}, Contracts: []config.Contract{{Name: "Token", Chain: "local", ABI: []byte(abi), Address: []string{address}, IncludeTransactionReceipts: true}}}, s, map[string]indexer.Handler{"Token:Transfer": func(ctx context.Context, c *indexer.Context) error {
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
	if views := os.Getenv("DATABASE_VIEWS_SCHEMA"); os.Getenv("PONDER_COMMAND") == "start" && views != "" {
		worker, e = indexer.PublishViewsOnReady(worker, s, views)
		if e != nil {
			return e
		}
	}
	return serve(ctx, worker, query.New(s))
}

func serve(ctx context.Context, worker indexer.ApplicationIndexer, server *query.Server) error {
	server.ChainNames[1] = "local"
	app, err := indexer.LaunchApplication(ctx, worker, server, "127.0.0.1", 42069)
	if err != nil {
		return err
	}
	log.Printf("local example at http://%s; indexing schema %s", app.Addr(), server.Store.Namespace)
	return app.Wait()
}
