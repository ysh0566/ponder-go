// An explicit local Ethereum + PostgreSQL example. See README before running.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"ponder.local/ponder-go/config"
	"ponder.local/ponder-go/query"
	indexer "ponder.local/ponder-go/runtime"
	"ponder.local/ponder-go/schema"
	"ponder.local/ponder-go/store"
	"syscall"
	"time"
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
	if os.Getenv("PONDER_COMMAND") != "serve" {
		if e = engine.Sync(ctx); e != nil {
			return e
		}
	}
	server.Ready.Store(true)
	httpServer := &http.Server{Addr: "127.0.0.1:42069", Handler: server, ReadHeaderTimeout: 5 * time.Second}
	errors := make(chan error, 2)
	go func() { errors <- httpServer.ListenAndServe() }()
	if os.Getenv("PONDER_COMMAND") != "serve" {
		go func() { errors <- engine.Run(ctx) }()
	}
	log.Printf("local example at http://127.0.0.1:42069; indexing schema %s", s.Namespace)
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	case e := <-errors:
		return e
	}
}
