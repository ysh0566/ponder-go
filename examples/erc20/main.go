// A local Ethereum indexer using PostgreSQL or a persistent PGlite directory.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/logging"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	indexer "github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

type networkConfig struct {
	Name       string     `json:"name"`
	ID         uint64     `json:"id"`
	Address    string     `json:"address"`
	StartBlock blockStart `json:"startBlock"`
	EndBlock   *uint64    `json:"endBlock"`
	rpcURL     string
	wsURL      string
}

type blockStart struct {
	height uint64
	latest bool
}

func (b *blockStart) UnmarshalJSON(raw []byte) error {
	var tag string
	if json.Unmarshal(raw, &tag) == nil && tag == "latest" {
		*b = blockStart{latest: true}
		return nil
	}
	var height *uint64
	if err := json.Unmarshal(raw, &height); err != nil || height == nil {
		return fmt.Errorf("startBlock must be a nonnegative integer or %q", "latest")
	}
	*b = blockStart{height: *height}
	return nil
}

func (n networkConfig) head(ctx context.Context) (uint64, error) {
	client := rpc.New(n.rpcURL)
	var id, head string
	if err := client.Call(ctx, "eth_chainId", []any{}, &id); err != nil {
		return 0, err
	}
	chainID, err := indexer.Number(id)
	if err != nil || chainID != n.ID {
		return 0, fmt.Errorf("expected chain %d, RPC returned %q", n.ID, id)
	}
	if err := client.Call(ctx, "eth_blockNumber", []any{}, &head); err != nil {
		return 0, err
	}
	return indexer.Number(head)
}

func (n networkConfig) chain() config.Chain {
	return config.Chain{Name: n.Name, ID: n.ID, RPC: []string{n.rpcURL}, WS: n.wsURL}
}

func (n networkConfig) contract(name string, eventABI []byte) config.Contract {
	return config.Contract{Name: name, Chain: n.Name, Address: []string{n.Address}, ABI: eventABI, StartBlock: n.StartBlock.height, EndBlock: n.EndBlock}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger, err := logging.New(os.Stderr, logging.Environment())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if e := run(ctx, logger); e != nil && ctx.Err() == nil {
		logger.Error(e.Error())
		os.Exit(1)
	}
}
func run(ctx context.Context, logger *logging.Logger) error {
	mode := os.Getenv("PONDER_COMMAND")
	rawNetwork, e := os.ReadFile("network.json")
	if e != nil {
		return e
	}
	networks, e := projectNetworks(rawNetwork)
	if e != nil {
		return fmt.Errorf("network.json: %w", e)
	}
	if len(networks) == 0 {
		return fmt.Errorf("network.json requires at least one chain")
	}
	for i := range networks {
		n := &networks[i]
		if n.Name == "" || n.ID == 0 {
			return fmt.Errorf("set a chain name and a positive chain id in network.json")
		}
		rpcEnv := fmt.Sprintf("PONDER_RPC_URL_%d", n.ID)
		n.rpcURL = cmp.Or(n.rpcURL, os.Getenv(rpcEnv))
		if mode != "serve" && n.rpcURL == "" {
			return fmt.Errorf("set %s to index events on %s", rpcEnv, n.Name)
		}
		if mode != "serve" && n.Address == "" {
			return fmt.Errorf("set the source address for %s in network.json", n.Name)
		}
	}
	url, directory := os.Getenv("PONDER_LOCAL_DATABASE_URL"), os.Getenv("PONDER_PGLITE_DIR")
	if url == "" && directory == "" {
		url = cmp.Or(os.Getenv("DATABASE_PRIVATE_URL"), os.Getenv("DATABASE_URL"))
	}
	var backend store.Backend
	switch {
	case url != "":
		backend, e = store.OpenPostgres(ctx, url)
	case mode == "serve":
		return fmt.Errorf("independent serve requires PostgreSQL; set PONDER_LOCAL_DATABASE_URL")
	default:
		backend, e = store.OpenPGlite(ctx, cmp.Or(directory, ".ponder/pglite"))
	}
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
	if mode == "serve" {
		s, err := store.OpenExisting(ctx, backend, model, namespace, projectOrdering)
		if err != nil {
			return err
		}
		server, err := query.NewAtNamespace(ctx, s, s.Namespace)
		if err != nil {
			return err
		}
		defer server.Close()
		return serve(ctx, nil, server, logger, networks)
	}
	open := store.New
	switch os.Getenv("PONDER_DATABASE_MODE") {
	case "", "create":
	case "existing":
		open = store.OpenExisting
	default:
		return fmt.Errorf("PONDER_DATABASE_MODE must be create or existing")
	}
	s, e := open(ctx, backend, model, namespace, projectOrdering)
	if e != nil {
		return e
	}
	if backend.Dialect() == "postgres" {
		if e = s.EnableQueryNotifications(ctx); e != nil {
			return e
		}
	}
	for i := range networks {
		n := &networks[i]
		if n.StartBlock.latest {
			n.StartBlock.height, e = s.ResolveStartBlock(ctx, n.ID, n.head)
			if e != nil {
				return fmt.Errorf("resolve latest start block for %s: %w", n.Name, e)
			}
		}
	}
	worker, e := newProjectIndexer(ctx, s, networks, logger)
	if e != nil {
		return e
	}
	if views := os.Getenv("DATABASE_VIEWS_SCHEMA"); mode == "start" && views != "" {
		worker, e = indexer.PublishViewsOnReady(worker, s, views)
		if e != nil {
			return e
		}
	}
	return serve(ctx, worker, query.New(s), logger, networks)
}

func serve(ctx context.Context, worker indexer.ApplicationIndexer, server *query.Server, logger *logging.Logger, networks []networkConfig) error {
	for _, n := range networks {
		server.ChainNames[n.ID] = n.Name
	}
	if err := configureAPI(server); err != nil {
		return err
	}
	app, err := indexer.LaunchApplication(ctx, worker, server, "127.0.0.1", 42069)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("local example at http://%s; indexing schema %s", app.Addr(), server.Store.Namespace), "address", app.Addr(), "schema", server.Store.Namespace)
	return app.Wait()
}
