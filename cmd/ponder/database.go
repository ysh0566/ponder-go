package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/ysh0566/ponder-go/store"
)

func manageDatabase(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("db list, prune or create-views required")
	}
	op := args[0]
	flags := flag.NewFlagSet("db "+op, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	url := flags.String("database-url", "", "PostgreSQL database URL")
	var ns, views string
	switch op {
	case "list":
	case "prune":
		flags.StringVar(&ns, "schema", "", "Limit pruning to this deployment")
	case "create-views":
		flags.StringVar(&ns, "schema", "", "Source indexing schema")
		flags.StringVar(&views, "views-schema", "", "Published views schema")
	default:
		return fmt.Errorf("unknown db command %q", op)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *url == "" {
		return fmt.Errorf("--database-url is required")
	}
	if op == "create-views" && (ns == "" || views == "") {
		return fmt.Errorf("db create-views requires --schema and --views-schema")
	}
	backend, err := store.OpenPostgres(ctx, *url)
	if err != nil {
		return err
	}
	defer backend.Close()
	switch op {
	case "list":
		r, err := backend.Query(ctx, "SELECT n.nspname AS schema FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.relname='_ponder_go_meta' AND starts_with(n.nspname,'ponder_go_') ORDER BY n.nspname")
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	case "prune":
		result, pruneErr := store.PruneDeployments(ctx, backend, ns)
		if pruneErr == nil || len(result) > 0 {
			if err := json.NewEncoder(out).Encode(result); err != nil {
				return errors.Join(pruneErr, err)
			}
		}
		return pruneErr
	default:
		return store.PublishViews(ctx, backend, ns, views)
	}
}
