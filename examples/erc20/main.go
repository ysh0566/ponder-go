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

func (n networkConfig) head(ctx context.Context, rpcURL string) (uint64, error) {
	client := rpc.New(rpcURL)
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
	var network networkConfig
	if e = json.Unmarshal(rawNetwork, &network); e != nil {
		return fmt.Errorf("network.json: %w", e)
	}
	if network.Name == "" || network.ID == 0 {
		return fmt.Errorf("set a chain name and a positive chain id in network.json")
	}
	rpcEnv := fmt.Sprintf("PONDER_RPC_URL_%d", network.ID)
	rpcURL := cmp.Or(os.Getenv("PONDER_LOCAL_RPC_URL"), os.Getenv(rpcEnv))
	network.Address = cmp.Or(os.Getenv("PONDER_LOCAL_CONTRACT"), network.Address)
	if mode != "serve" && rpcURL == "" {
		return fmt.Errorf("set %s or PONDER_LOCAL_RPC_URL to index events", rpcEnv)
	}
	if mode != "serve" && network.Address == "" {
		return fmt.Errorf("set the source address in network.json or PONDER_LOCAL_CONTRACT")
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
		s, err := store.OpenExisting(ctx, backend, model, namespace, schema.Multichain)
		if err != nil {
			return err
		}
		server, err := query.NewAtNamespace(ctx, s, s.Namespace)
		if err != nil {
			return err
		}
		defer server.Close()
		return serve(ctx, nil, server, logger, network)
	}
	open := store.New
	switch os.Getenv("PONDER_DATABASE_MODE") {
	case "", "create":
	case "existing":
		open = store.OpenExisting
	default:
		return fmt.Errorf("PONDER_DATABASE_MODE must be create or existing")
	}
	s, e := open(ctx, backend, model, namespace, schema.Multichain)
	if e != nil {
		return e
	}
	if backend.Dialect() == "postgres" {
		if e = s.EnableQueryNotifications(ctx); e != nil {
			return e
		}
	}
	chain := config.Chain{Name: network.Name, ID: network.ID, RPC: []string{rpcURL}}
	if network.StartBlock.latest {
		network.StartBlock.height, e = s.ResolveStartBlock(ctx, network.ID, func(ctx context.Context) (uint64, error) {
			return network.head(ctx, rpcURL)
		})
		if e != nil {
			return fmt.Errorf("resolve latest start block: %w", e)
		}
	}
	worker, e := newProjectIndexer(ctx, s, network, chain, logger)
	if e != nil {
		return e
	}
	if views := os.Getenv("DATABASE_VIEWS_SCHEMA"); mode == "start" && views != "" {
		worker, e = indexer.PublishViewsOnReady(worker, s, views)
		if e != nil {
			return e
		}
	}
	return serve(ctx, worker, query.New(s), logger, network)
}

func serve(ctx context.Context, worker indexer.ApplicationIndexer, server *query.Server, logger *logging.Logger, network networkConfig) error {
	server.ChainNames[network.ID] = network.Name
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
