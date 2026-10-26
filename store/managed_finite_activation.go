package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ponder.local/ponder-go/schema"
)

type managedActivationStage struct {
	name    string
	apply   func(context.Context, Transaction) error
	catalog func(context.Context, Querier) (any, error)
}

// Activate is intentionally finite-only. The caller must first seal admission
// and join historical descendants. Every stage rechecks durable exact endpoints
// and real owner fencing. A failed stage leaves earlier committed stages intact.
func (m *ManagedFinite) Activate(ctx context.Context, expected []CheckpointState) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !m.mu.TryLock() {
		return ErrManagedFiniteBusy
	}
	defer m.mu.Unlock()
	expected = append([]CheckpointState(nil), expected...)
	if len(expected) != len(m.opts.Chains) {
		return fmt.Errorf("managed activation requires exact endpoint targets")
	}
	stages := m.activationStages()
	for _, stage := range stages {
		if err := m.runStage(ctx, stage, expected, nil); err != nil {
			return err
		}
	}
	ready := managedActivationStage{name: "ready", apply: func(ctx context.Context, tx Transaction) error {
		r, e := tx.Query(ctx, "UPDATE "+m.s.internal("_ponder_go_app")+" SET value=jsonb_set(value,'{is_ready}','1'::jsonb) WHERE key='app' AND lease_token=$1 AND value->>'is_locked'='1' RETURNING key", m.s.leaseToken)
		if e != nil {
			return e
		}
		if len(r.Rows) != 1 {
			return ErrLeaseLost
		}
		return nil
	}, catalog: func(ctx context.Context, q Querier) (any, error) {
		r, e := q.Query(ctx, "SELECT value->>'is_ready' AS ready,value->>'is_locked' AS locked,lease_token FROM "+m.s.internal("_ponder_go_app")+" WHERE key='app'")
		if e != nil {
			return nil, e
		}
		if len(r.Rows) != 1 || r.Rows[0]["ready"] != "1" || r.Rows[0]["locked"] != "1" || r.Rows[0]["lease_token"] != m.s.leaseToken {
			return nil, fmt.Errorf("managed ready metadata not confirmed")
		}
		return map[string]any{"ready": true, "identity": m.identity}, nil
	}}
	return m.runStage(ctx, ready, expected, stages)
}

func (m *ManagedFinite) runStage(ctx context.Context, stage managedActivationStage, expected []CheckpointState, prior []managedActivationStage) error {
	if e := m.lockStage(ctx); e != nil {
		return e
	}
	defer m.s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := m.s.Backend.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = m.s.lock(ctx, tx); err != nil {
		return err
	}
	if err = m.requireLease(ctx, tx); err != nil {
		return err
	}
	if err = m.verifyBoundary(ctx, tx, expected, false); err != nil {
		return err
	}
	for _, p := range prior {
		present, e := m.verifyStage(ctx, tx, p)
		if e != nil {
			return e
		}
		if !present {
			return fmt.Errorf("managed stage %s is missing before ready", p.name)
		}
	}
	present, err := m.verifyStage(ctx, tx, stage)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	if err = m.requireStageAbsent(ctx, tx, stage.name); err != nil {
		return err
	}
	if err = stage.apply(ctx, tx); err != nil {
		return err
	}
	catalog, err := stage.catalog(ctx, tx)
	if err != nil {
		return err
	}
	if err = m.requireLease(ctx, tx); err != nil {
		return err
	}
	if err = tx.Exec(ctx, "INSERT INTO "+m.s.internal("_ponder_go_managed_stages")+" (name,owner_token,catalog_hash) VALUES ($1,$2,$3)", stage.name, m.s.leaseToken, managedHash(catalog)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err == nil {
		return nil
	}
	commitErr := err
	_ = tx.Rollback(ctx)
	// Reconciliation keeps the original caller context/deadline. It never masks
	// cancellation or reconnects the fixed owner session after connection loss.
	if ctx.Err() != nil {
		return &ManagedStageError{Stage: stage.name, Outcome: "unknown", Err: errors.Join(commitErr, ctx.Err())}
	}
	read, readErr := m.s.Backend.Begin(ctx)
	if readErr != nil {
		return &ManagedStageError{Stage: stage.name, Outcome: "unknown", Err: errors.Join(commitErr, readErr)}
	}
	defer read.Rollback(context.Background())
	if readErr = m.requireLease(ctx, read); readErr == nil {
		readErr = m.verifyBoundary(ctx, read, expected, false)
	}
	if readErr != nil {
		return &ManagedStageError{Stage: stage.name, Outcome: "unknown", Err: errors.Join(commitErr, readErr)}
	}
	found, readErr := m.verifyStage(ctx, read, stage)
	if readErr != nil {
		return &ManagedStageError{Stage: stage.name, Outcome: "unknown", Err: errors.Join(commitErr, readErr)}
	}
	if found {
		return nil
	}
	return &ManagedStageError{Stage: stage.name, Outcome: "not_committed", Err: commitErr}
}
func (m *ManagedFinite) verifyStage(ctx context.Context, q Querier, stage managedActivationStage) (bool, error) {
	r, e := q.Query(ctx, "SELECT owner_token,catalog_hash FROM "+m.s.internal("_ponder_go_managed_stages")+" WHERE name=$1", stage.name)
	if e != nil {
		return false, e
	}
	if len(r.Rows) == 0 {
		return false, nil
	}
	if len(r.Rows) != 1 || r.Rows[0]["owner_token"] != m.s.leaseToken {
		return false, ErrLeaseLost
	}
	actual, e := stage.catalog(ctx, q)
	if e != nil {
		return false, e
	}
	if r.Rows[0]["catalog_hash"] != managedHash(actual) {
		return false, fmt.Errorf("managed %s catalog changed after confirmation", stage.name)
	}
	return true, nil
}
func (m *ManagedFinite) activationStages() []managedActivationStage {
	stages := []managedActivationStage{}
	for _, table := range m.s.Schema.Tables {
		for _, index := range table.Indexes {
			t, i := table, index
			stages = append(stages, managedActivationStage{name: "index:" + t.Name + ":" + i.Name, apply: func(ctx context.Context, tx Transaction) error {
				if err := tx.Exec(ctx, "SET LOCAL statement_timeout='3600000ms'"); err != nil {
					return err
				}
				cols := []string{}
				for _, c := range i.Columns {
					cols = append(cols, schema.Quote(c))
				}
				unique := ""
				if i.Unique {
					unique = "UNIQUE "
				}
				return tx.Exec(ctx, "CREATE "+unique+"INDEX "+schema.Quote(i.Name)+" ON "+m.s.table(t.Name)+" ("+strings.Join(cols, ",")+")")
			}, catalog: func(ctx context.Context, q Querier) (any, error) { return m.indexCatalog(ctx, q, t, i) }})
		}
	}
	stages = append(stages, managedActivationStage{name: "capture", apply: m.s.createJournalTriggers, catalog: m.captureCatalog})
	stages = append(stages, managedActivationStage{name: "live-query", apply: func(ctx context.Context, tx Transaction) error {
		a := &Actions{m.s}
		if e := a.createLiveQueryProcedures(ctx, tx); e != nil {
			return e
		}
		for _, t := range m.s.Schema.Tables {
			if e := tx.Exec(ctx, "CREATE OR REPLACE TRIGGER live_query AFTER INSERT OR UPDATE OR DELETE ON "+m.s.table(t.Name)+" FOR EACH ROW EXECUTE FUNCTION "+m.s.internal("live_query")+"()"); e != nil {
				return e
			}
		}
		for _, n := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
			if e := tx.Exec(ctx, "CREATE OR REPLACE TRIGGER live_query_notify AFTER INSERT OR UPDATE OR DELETE ON "+m.s.internal(n)+" FOR EACH STATEMENT EXECUTE FUNCTION "+m.s.internal("live_query_notify")+"()"); e != nil {
				return e
			}
		}
		return nil
	}, catalog: m.liveCatalog})
	if m.opts.ViewsSchema != "" {
		stages = append(stages, managedActivationStage{name: "aliases", apply: m.s.createAliasViews, catalog: m.aliasCatalog})
	}
	return stages
}
func (m *ManagedFinite) indexCatalog(ctx context.Context, q Querier, t schema.Table, i schema.Index) (any, error) {
	r, e := q.Query(ctx, `SELECT tn.nspname AS table_schema,am.amname AS method,ix.indnkeyatts=ix.indnatts AS no_include,NOT EXISTS(SELECT 1 FROM unnest(ix.indoption) opt WHERE opt<>0) AS default_order,NOT EXISTS(SELECT 1 FROM unnest(ix.indclass) op JOIN pg_opclass oc ON oc.oid=op WHERE NOT oc.opcdefault) AS default_opclass,NOT EXISTS(SELECT 1 FROM unnest(ix.indkey,ix.indcollation) AS k(attnum,coll_oid) JOIN pg_attribute a ON a.attrelid=tab.oid AND a.attnum=k.attnum WHERE k.coll_oid<>a.attcollation) AS default_collation,tab.relname AS table_name,ix.indisunique AS unique,ix.indisvalid AS valid,ix.indisready AS ready,ix.indpred IS NULL AS no_predicate,ix.indexprs IS NULL AS no_expression,pg_get_indexdef(idx.oid) AS definition,array_to_json(ARRAY(SELECT a.attname FROM unnest(ix.indkey) WITH ORDINALITY AS k(attnum,ord) JOIN pg_attribute a ON a.attrelid=tab.oid AND a.attnum=k.attnum ORDER BY k.ord))::text AS columns FROM pg_index ix JOIN pg_class idx ON idx.oid=ix.indexrelid JOIN pg_namespace n ON n.oid=idx.relnamespace JOIN pg_class tab ON tab.oid=ix.indrelid JOIN pg_namespace tn ON tn.oid=tab.relnamespace JOIN pg_am am ON am.oid=idx.relam WHERE n.nspname=$1 AND idx.relname=$2`, m.s.Namespace, i.Name)
	if e != nil {
		return nil, e
	}
	if len(r.Rows) != 1 {
		return nil, fmt.Errorf("managed index missing: %s", i.Name)
	}
	row := r.Rows[0]
	var cols []string
	if e = json.Unmarshal([]byte(fmt.Sprint(row["columns"])), &cols); e != nil {
		return nil, e
	}
	if row["table_schema"] != m.s.Namespace || row["method"] != "btree" || row["no_include"] != true || row["default_order"] != true || row["default_opclass"] != true || row["default_collation"] != true || row["table_name"] != t.Name || row["unique"] != i.Unique || row["valid"] != true || row["ready"] != true || row["no_predicate"] != true || row["no_expression"] != true || strings.Join(cols, "\x00") != strings.Join(i.Columns, "\x00") {
		return nil, fmt.Errorf("managed index definition mismatch: %s", i.Name)
	}
	return row, nil
}
func (m *ManagedFinite) triggerCatalog(ctx context.Context, q Querier, table, name, functionNamespace, function string, kind int, args []string) (any, error) {
	r, e := q.Query(ctx, `SELECT t.tgtype::int AS kind,t.tgenabled::text AS enabled,p.proname AS function,n.nspname AS function_schema,encode(t.tgargs,'hex') AS args,pg_get_triggerdef(t.oid) AS definition,pg_get_functiondef(p.oid) AS function_definition FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace cn ON cn.oid=c.relnamespace JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE cn.nspname=$1 AND c.relname=$2 AND t.tgname=$3 AND NOT t.tgisinternal`, m.s.Namespace, table, name)
	if e != nil {
		return nil, e
	}
	if len(r.Rows) != 1 {
		return nil, fmt.Errorf("managed trigger missing: %s.%s", table, name)
	}
	raw := ""
	for _, arg := range args {
		raw += arg + "\x00"
	}
	row := r.Rows[0]
	if fmt.Sprint(row["kind"]) != fmt.Sprint(kind) || row["enabled"] != "O" || row["function"] != function || row["function_schema"] != functionNamespace || row["args"] != hex.EncodeToString([]byte(raw)) {
		return nil, fmt.Errorf("managed trigger mismatch: %s.%s", table, name)
	}
	return row, nil
}
func (m *ManagedFinite) captureCatalog(ctx context.Context, q Querier) (any, error) {
	definition, e := m.functionCatalog(ctx, q, m.s.Namespace, "_ponder_go_capture")
	if e != nil {
		return nil, e
	}
	out := map[string]any{"$function": definition}
	for _, t := range m.s.Schema.Tables {
		for _, x := range []struct {
			name string
			kind int
		}{{"_ponder_go_capture", 29}, {"_ponder_go_no_truncate", 32}} {
			v, e := m.triggerCatalog(ctx, q, t.Name, x.name, m.s.Namespace, "_ponder_go_capture", x.kind, t.Keys())
			if e != nil {
				return nil, e
			}
			out[t.Name+":"+x.name] = v
		}
	}
	return out, nil
}
func (m *ManagedFinite) liveCatalog(ctx context.Context, q Querier) (any, error) {
	definition, e := m.functionCatalog(ctx, q, m.s.Namespace, "live_query")
	if e != nil {
		return nil, e
	}
	out := map[string]any{"$function": definition}
	for _, t := range m.s.Schema.Tables {
		v, e := m.triggerCatalog(ctx, q, t.Name, "live_query", m.s.Namespace, "live_query", 29, nil)
		if e != nil {
			return nil, e
		}
		out[t.Name] = v
	}
	for _, n := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
		v, e := m.triggerCatalog(ctx, q, n, "live_query_notify", m.s.Namespace, "live_query_notify", 28, nil)
		if e != nil {
			return nil, e
		}
		out[n] = v
	}
	return out, nil
}
func (m *ManagedFinite) aliasCatalog(ctx context.Context, q Querier) (any, error) {
	if e := m.verifyAliasOwnership(ctx, q); e != nil {
		return nil, e
	}
	out := map[string]any{}
	names := []string{"_ponder_meta", "_ponder_checkpoint"}
	for _, t := range m.s.Schema.Tables {
		names = append(names, t.Name)
	}
	for _, v := range m.s.Schema.Views {
		names = append(names, v.Name)
	}
	for _, n := range names {
		r, e := q.Query(ctx, "SELECT c.relkind::text AS kind,pg_get_viewdef(c.oid) AS definition FROM pg_class c JOIN pg_namespace ns ON ns.oid=c.relnamespace WHERE ns.nspname=$1 AND c.relname=$2", m.opts.ViewsSchema, n)
		if e != nil {
			return nil, e
		}
		if len(r.Rows) != 1 || r.Rows[0]["kind"] != "v" {
			return nil, fmt.Errorf("managed alias view missing: %s", n)
		}
		out[n] = r.Rows[0]
	}
	for _, n := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
		v, e := m.triggerCatalog(ctx, q, n, m.opts.ViewsSchema+"_live_query_notify", m.opts.ViewsSchema, "live_query_notify", 28, nil)
		if e != nil {
			return nil, e
		}
		out["trigger:"+n] = v
	}
	return out, nil
}

// No unmarked object is adopted, even if it happens to have a familiar name.
func (m *ManagedFinite) requireStageAbsent(ctx context.Context, q Querier, name string) error {
	checks := []struct {
		query string
		args  []any
	}{}
	relation := func(ns, n string) {
		checks = append(checks, struct {
			query string
			args  []any
		}{"SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2", []any{ns, n}})
	}
	trigger := func(table, n string) {
		checks = append(checks, struct {
			query string
			args  []any
		}{"SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND t.tgname=$3", []any{m.s.Namespace, table, n}})
	}
	function := func(ns, n string) {
		checks = append(checks, struct {
			query string
			args  []any
		}{"SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2", []any{ns, n}})
	}
	switch {
	case strings.HasPrefix(name, "index:"):
		parts := strings.Split(name, ":")
		relation(m.s.Namespace, parts[2])
	case name == "capture":
		for _, t := range m.s.Schema.Tables {
			trigger(t.Name, "_ponder_go_capture")
			trigger(t.Name, "_ponder_go_no_truncate")
		}
		function(m.s.Namespace, "_ponder_go_capture")
	case name == "live-query":
		for _, t := range m.s.Schema.Tables {
			trigger(t.Name, "live_query")
		}
		for _, n := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
			trigger(n, "live_query_notify")
		}
		function(m.s.Namespace, "live_query")
		function(m.s.Namespace, "live_query_notify")
	case name == "aliases":
		for _, t := range m.s.Schema.Tables {
			relation(m.opts.ViewsSchema, t.Name)
		}
		for _, v := range m.s.Schema.Views {
			relation(m.opts.ViewsSchema, v.Name)
		}
		relation(m.opts.ViewsSchema, "_ponder_meta")
		relation(m.opts.ViewsSchema, "_ponder_checkpoint")
		for _, n := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
			trigger(n, m.opts.ViewsSchema+"_live_query_notify")
		}
		function(m.opts.ViewsSchema, "live_query_notify")
	}
	for _, c := range checks {
		r, e := q.Query(ctx, c.query, c.args...)
		if e != nil {
			return e
		}
		if len(r.Rows) > 0 {
			return fmt.Errorf("unmarked managed stage object already exists: %s", name)
		}
	}
	return nil
}

func (m *ManagedFinite) functionCatalog(ctx context.Context, q Querier, namespace, name string) (any, error) {
	r, e := q.Query(ctx, "SELECT pg_get_functiondef(p.oid) AS definition FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname=$2 AND p.pronargs=0", namespace, name)
	if e != nil {
		return nil, e
	}
	if len(r.Rows) != 1 {
		return nil, fmt.Errorf("managed function missing or ambiguous: %s.%s", namespace, name)
	}
	return r.Rows[0], nil
}

// Stage admission waits cooperatively so brief metadata reads do not abort an
// activation, but a held historical transaction cannot hide caller cancellation.
func (m *ManagedFinite) lockStage(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if m.s.mu.TryLock() {
			if e := ctx.Err(); e != nil {
				m.s.mu.Unlock()
				return e
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
