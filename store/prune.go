package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ysh0566/ponder-go/schema"
)

// PrunedDeployment describes the outcome for a single current native store.
type PrunedDeployment struct {
	Schema string `json:"schema"`
	Status string `json:"status"`
}

// PruneDeployments removes inactive, unpublished deployments. It uses separate
// transactions per deployment and PostgreSQL's RESTRICT dependency checks.
func PruneDeployments(ctx context.Context, backend Backend, namespace string) ([]PrunedDeployment, error) {
	var names []string
	if namespace != "" {
		names = []string{namespace}
	} else {
		list, err := backend.Query(ctx, "SELECT n.nspname AS name FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE starts_with(n.nspname,'ponder_go_') AND c.relname='_ponder_go_meta' AND c.relkind='r' AND c.relpersistence='p' AND EXISTS(SELECT FROM pg_class j WHERE j.relnamespace=n.oid AND j.relname='_ponder_go_journal') ORDER BY n.nspname")
		if err != nil {
			return nil, err
		}
		for _, row := range list.Rows {
			names = append(names, fmt.Sprint(row["name"]))
		}
	}
	out := []PrunedDeployment{}
	for _, ns := range names {
		status, err := pruneOne(ctx, backend, ns)
		if err != nil {
			return out, fmt.Errorf("prune %q: %w", ns, err)
		}
		out = append(out, PrunedDeployment{ns, status})
	}
	return out, nil
}

func pruneOne(ctx context.Context, b Backend, ns string) (string, error) {
	if !schema.Identifier(ns) || len(ns) > 45 || !strings.HasPrefix(ns, "ponder_go_") {
		return "", fmt.Errorf("invalid deployment name")
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	locks, err := tx.Query(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,0)) AS available", ns)
	if err != nil {
		return "", err
	}
	if locks.Rows[0]["available"] != true {
		return "busy", nil
	}
	locks, err = tx.Query(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,1)) AS idle", ns)
	if err != nil {
		return "", err
	}
	if locks.Rows[0]["idle"] != true {
		return "active", nil
	}
	found, err := tx.Query(ctx, "SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname=$1) AS present,  EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='_ponder_go_meta' AND c.relkind='r') AS registered", ns)
	if err != nil {
		return "", err
	}
	if found.Rows[0]["present"] != true {
		return "absent", nil
	}
	if found.Rows[0]["registered"] != true {
		return "unregistered", nil
	}
	s, err := readDeployment(ctx, b, tx, ns)
	if errors.Is(err, errUnregisteredDeployment) {
		return "unregistered", nil
	}
	if err != nil {
		return "", err
	}
	published, err := tx.Query(ctx, "SELECT EXISTS (SELECT FROM pg_namespace WHERE obj_description(oid,'pg_namespace')=$1) AS published", s.aliasOwner())
	if err != nil {
		return "", err
	}
	if published.Rows[0]["published"] == true {
		return "published", nil
	}
	if s.leaseTimeout > 0 {
		active, err := tx.Query(ctx, "SELECT EXISTS(SELECT FROM "+s.internal("_ponder_go_app")+" WHERE key='app' AND value->>'is_locked'='1' AND (value->>'heartbeat_at')::bigint+$1::bigint > "+databaseMillis+") AS active", s.leaseTimeout)
		if err != nil {
			return "", err
		}
		if active.Rows[0]["active"] == true {
			return "active", nil
		}
	}
	if err = s.removeDeployment(ctx, tx); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return "pruned", nil
}

func (s *Store) removeDeployment(ctx context.Context, tx Transaction) error {
	drop := func(kind string, objects []string) error {
		if len(objects) == 0 {
			return nil
		}
		return tx.Exec(ctx, "DROP "+kind+" IF EXISTS "+strings.Join(objects, ",")+" RESTRICT")
	}
	var views, tables, types []string
	for _, v := range s.Schema.Views {
		if !v.Existing {
			views = append(views, s.table(v.Name))
		}
	}
	for _, t := range s.Schema.Tables {
		tables = append(tables, s.table(t.Name))
	}
	for _, enum := range s.Schema.Enums {
		types = append(types, s.table(enum.Name))
	}
	if err := drop("VIEW", views); err != nil {
		return err
	}
	for _, n := range []string{"_ponder_go_journal", "_ponder_go_chains", "_ponder_go_events", "_ponder_go_progress", "_ponder_go_runtime_cursor", "_ponder_go_finalized", "_ponder_go_meta"} {
		tables = append(tables, s.internal(n))
	}
	if s.leaseTimeout > 0 {
		tables = append(tables, s.internal("_ponder_go_app"))
	}
	for _, n := range []string{"blocks", "intervals", "rpc_cache", "factory_addresses", "_ponder_go_meta"} {
		tables = append(tables, s.syncTable(n))
	}
	if err := drop("TABLE", tables); err != nil {
		return err
	}
	functions := []string{s.internal("_ponder_go_capture") + "()", s.internal("_ponder_go_guard") + "()"}
	if s.staged {
		functions = append(functions, s.internal("live_query")+"()", s.internal("live_query_notify")+"()")
	}
	if err := drop("FUNCTION", functions); err != nil {
		return err
	}
	if err := drop("TYPE", types); err != nil {
		return err
	}
	return drop("SCHEMA", []string{schema.Quote(s.Namespace), schema.Quote(s.SyncNamespace)})
}
