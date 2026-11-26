// Command managed-multichain indexes one row per block in two independent storage domains.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ysh0566/ponder-go/config"
	"github.com/ysh0566/ponder-go/historical"
	"github.com/ysh0566/ponder-go/indexing"
	"github.com/ysh0566/ponder-go/query"
	"github.com/ysh0566/ponder-go/rpc"
	"github.com/ysh0566/ponder-go/runtime"
	"github.com/ysh0566/ponder-go/schema"
	"github.com/ysh0566/ponder-go/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func recordBlock(ctx context.Context, args *indexing.HandlerArgs) error {
	chain, _, err := args.Context.ChainIdentity()
	if err != nil {
		return err
	}
	_, err = args.Context.DB.InsertOne(ctx, "indexed_block", store.Row{
		"id": args.Event.Get("id"), "chain": chain,
	}, store.Conflict{})
	return err
}

func run(ctx context.Context) (err error) {
	namespace := cmp.Or(os.Getenv("DATABASE_SCHEMA"), "ponder_go_blocks")
	storage := store.IndependentStorageOptions{
		BusinessNamespace: namespace, SyncNamespace: namespace + "_cache",
		Schema: schema.Schema{Tables: []schema.Table{{Name: "indexed_block", Columns: []schema.Column{
			{Name: "id", Type: "text", Primary: true}, {Name: "chain", Type: "text", NotNull: true},
		}}}},
		PostgresURL: cmp.Or(os.Getenv("DATABASE_PRIVATE_URL"), os.Getenv("DATABASE_URL")),
	}
	buildID := "managed-blocks-v1"
	views := os.Getenv("DATABASE_VIEWS_SCHEMA")
	mode := os.Getenv("PONDER_COMMAND")
	switch mode {
	case "serve":
		return serveExisting(ctx, storage)
	case "dev":
		buildID = os.Getenv("PONDER_DEV_REVISION")
		if buildID == "" {
			return fmt.Errorf("launch the development indexer with ponder dev")
		}
		// The CLI shadow can already occupy all 45 namespace characters.
		storage.SyncNamespace = fmt.Sprintf("ponder_go_%.20s_cache", buildID)
		views = ""
	case "", "start":
	default:
		return fmt.Errorf("use ponder dev, start or serve for this project")
	}
	raw, err := os.ReadFile("ponder.json")
	if err != nil {
		return err
	}
	src, err := config.ParseSourceConfiguration(raw)
	if err != nil {
		return err
	}
	buildOptions := config.SourceBuildOptions{RPCMaxConcurrency: 4}
	base, err := config.BuildSourceConfig(src, buildOptions)
	if err != nil {
		return err
	}
	build, err := config.BuildSourceIndexing(ctx, src, base, []config.SourceIndexingFunction{
		{Name: "Head:block", Fn: indexing.Handler(recordBlock)},
	})
	if err != nil {
		return err
	}
	open := historical.OpenRuntimePostgres
	if storage.PostgresURL == "" {
		root := filepath.Join(cmp.Or(os.Getenv("PONDER_PGLITE_DIR"), ".ponder"), namespace)
		storage.BusinessDirectory, storage.SyncDirectory = filepath.Join(root, "business"), filepath.Join(root, "sync")
		open = historical.OpenRuntimePGlite
	}
	runner, err := open(ctx, historical.RuntimeOptions{
		Development: mode == "dev", BuildID: buildID, OpenID: rand.Text(), ViewsSchema: views,
		Live: &historical.RuntimeLiveOptions{}, SourceCheckpoints: true,
		Historical: historical.Options{Build: build, Owned: indexing.OwnedOptions{Storage: storage, Service: indexing.Options{Errors: &indexing.ErrorState{}}}},
		Requesters: func(context.Context, uint64) ([]rpc.Requester, error) {
			fresh, err := config.BuildSourceConfig(src, buildOptions)
			if err != nil {
				return nil, err
			}
			var clients []rpc.Requester
			for _, client := range fresh.RPCClients() {
				clients = append(clients, client)
			}
			return clients, nil
		},
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runner.Close(context.Background())) }()
	routes, err := runner.QueryServer(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, routes.Close()) }()
	return serve(ctx, runner, routes)
}

func serveExisting(ctx context.Context, storage store.IndependentStorageOptions) (err error) {
	if storage.PostgresURL == "" {
		return fmt.Errorf("set DATABASE_URL or DATABASE_PRIVATE_URL to a PostgreSQL connection for ponder serve")
	}
	db, err := store.OpenPostgres(ctx, storage.PostgresURL)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	routes, err := query.OpenManaged(ctx, db, storage.Schema, storage.BusinessNamespace, schema.Multichain)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, routes.Close()) }()
	return serve(ctx, nil, routes)
}

func serve(ctx context.Context, indexer runtime.ApplicationIndexer, routes *query.Server) error {
	app, err := runtime.LaunchApplication(ctx, indexer, routes, "127.0.0.1", 42069)
	if err != nil {
		return err
	}
	fmt.Printf("Listening on http://%s\n", app.Addr())
	return app.Wait()
}
