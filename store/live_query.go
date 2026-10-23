package store

import (
	"context"

	"ponder.local/ponder-go/schema"
)

// Matches the retained TS client's database notification channel convention.
func (a *Actions) LiveQueryChannel() string { return a.s.Namespace + "_live_query" }

// PostgreSQL drops the transaction's collected tables on commit. The existing
// PGlite session reuses and clears its temp table, as the fixed source does.
func prepareLiveQueryChanges(ctx context.Context, tx Transaction, dialect string) error {
	if dialect == "postgres" {
		if e := tx.Exec(ctx, "DROP TABLE IF EXISTS pg_temp.live_query_tables"); e != nil {
			return e
		}
		return tx.Exec(ctx, "CREATE TEMP TABLE live_query_tables (table_name TEXT PRIMARY KEY) ON COMMIT DROP")
	}
	if e := tx.Exec(ctx, "CREATE TEMP TABLE IF NOT EXISTS live_query_tables (table_name TEXT PRIMARY KEY)"); e != nil {
		return e
	}
	return tx.Exec(ctx, "TRUNCATE TABLE pg_temp.live_query_tables")
}

// Installs actual database procedures; no in-process notification substitute.
// This is explicit setup, like the source database setup service's procedures.
func (a *Actions) CreateLiveQueryProcedures(ctx context.Context) error {
	return a.transaction(ctx, func(tx Transaction) error { return a.createLiveQueryProcedures(ctx, tx) })
}
func (a *Actions) createLiveQueryProcedures(ctx context.Context, tx Transaction) error {
	if e := tx.Exec(ctx, `CREATE OR REPLACE FUNCTION `+a.s.internal("live_query")+`() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO pg_temp.live_query_tables (table_name) VALUES (TG_TABLE_NAME)
 ON CONFLICT (table_name) DO NOTHING;
 RETURN NULL;
END; $$`); e != nil {
		return e
	}
	// The 7900-byte boundary, JSON escaping, sorted iteration and final empty
	// array follow getLiveQueryNotifyProcedureSql at the pinned revision.
	return tx.Exec(ctx, liveQueryNotifySQL(a.s.Namespace, a.LiveQueryChannel()))
}
func liveQueryNotifySQL(namespace, channel string) string {
	return `CREATE OR REPLACE FUNCTION ` + schema.Quote(namespace) + `."live_query_notify"() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
 current_payload text := '[';
 separator text := '';
 entry text;
 entry_json text;
BEGIN
 IF to_regclass('pg_temp.live_query_tables') IS NULL THEN RETURN NULL; END IF;
 FOR entry IN SELECT table_name FROM pg_temp.live_query_tables ORDER BY table_name LOOP
  entry_json := to_json(entry)::text;
  IF current_payload <> '[' AND octet_length(current_payload) + octet_length(separator) + octet_length(entry_json) + 1 >= 7900 THEN
   PERFORM pg_notify('` + channel + `', current_payload || ']');
   current_payload := '[' || entry_json;
   separator := ',';
  ELSE
   current_payload := current_payload || separator || entry_json;
   separator := ',';
  END IF;
 END LOOP;
 IF current_payload = '[' THEN
  PERFORM pg_notify('` + channel + `', '[]');
 ELSE
  PERFORM pg_notify('` + channel + `', current_payload || ']');
 END IF;
 RETURN NULL;
END; $$`
}

// tables selects declared business tables, including the empty selection used
// by the source's large-payload fixture. Native checkpoint state is split over
// progress/finalized, so both relations receive the statement-level trigger.
func (a *Actions) CreateLiveQueryTriggers(ctx context.Context, tables []string) error {
	return a.transaction(ctx, func(tx Transaction) error {
		for _, name := range tables {
			if _, e := a.s.model(name); e != nil {
				return e
			}
			if e := tx.Exec(ctx, "CREATE OR REPLACE TRIGGER live_query AFTER INSERT OR UPDATE OR DELETE ON "+a.s.table(name)+" FOR EACH ROW EXECUTE FUNCTION "+a.s.internal("live_query")+"()"); e != nil {
				return e
			}
		}
		for _, relation := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
			if e := tx.Exec(ctx, "CREATE OR REPLACE TRIGGER live_query_notify AFTER INSERT OR UPDATE OR DELETE ON "+a.s.internal(relation)+" FOR EACH STATEMENT EXECUTE FUNCTION "+a.s.internal("live_query_notify")+"()"); e != nil {
				return e
			}
		}
		return nil
	})
}

func (a *Actions) DropLiveQueryTriggers(ctx context.Context, tables []string) error {
	return a.transaction(ctx, func(tx Transaction) error {
		for _, name := range tables {
			if _, e := a.s.model(name); e != nil {
				return e
			}
			if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS "+schema.Quote("live_query")+" ON "+a.s.table(name)); e != nil {
				return e
			}
		}
		for _, relation := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
			if e := tx.Exec(ctx, "DROP TRIGGER IF EXISTS live_query_notify ON "+a.s.internal(relation)); e != nil {
				return e
			}
		}
		return nil
	})
}
