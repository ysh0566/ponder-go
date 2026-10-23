package store

import (
	"context"
	"fmt"

	"ponder.local/ponder-go/schema"
)

func (a *Actions) ViewsSchema() string { return a.s.viewsSchema }
func (a *Actions) ViewsLiveQueryChannel() string {
	if a.s.viewsSchema == "" {
		return ""
	}
	return a.s.viewsSchema + "_live_query"
}
func (s *Store) aliasOwner() string { return "ponder-go:views:v1:" + s.Namespace }
func (s *Store) aliasTable(name string) string {
	return schema.Quote(s.viewsSchema) + "." + schema.Quote(name)
}
func (s *Store) createAliasViews(ctx context.Context, tx Transaction) error {
	if s.viewsSchema == "" {
		return nil
	}
	r, e := tx.Query(ctx, "SELECT obj_description(oid,'pg_namespace') AS owner FROM pg_namespace WHERE nspname=$1", s.viewsSchema)
	if e != nil {
		return e
	}
	if len(r.Rows) > 0 && r.Rows[0]["owner"] != s.aliasOwner() {
		return fmt.Errorf("refusing to adopt unowned views schema %q", s.viewsSchema)
	}
	if len(r.Rows) == 0 {
		if e = tx.Exec(ctx, "CREATE SCHEMA "+schema.Quote(s.viewsSchema)); e != nil {
			return e
		}
		if e = tx.Exec(ctx, "COMMENT ON SCHEMA "+schema.Quote(s.viewsSchema)+" IS '"+s.aliasOwner()+"'"); e != nil {
			return e
		}
	}
	for _, table := range s.Schema.Tables {
		if e = s.aliasView(ctx, tx, table.Name, "SELECT * FROM "+s.table(table.Name)); e != nil {
			return e
		}
	}
	for _, view := range s.Schema.Views {
		if e = s.aliasView(ctx, tx, view.Name, "SELECT * FROM "+s.table(view.Name)); e != nil {
			return e
		}
	}
	// Expose source-shaped metadata without the native fencing token.
	if e = s.aliasView(ctx, tx, "_ponder_meta", "SELECT key,value FROM "+s.internal("_ponder_go_app")); e != nil {
		return e
	}
	if e = s.aliasView(ctx, tx, "_ponder_checkpoint", "SELECT c.chain_name,c.chain_id::bigint AS chain_id,f.safe_checkpoint,p.checkpoint AS latest_checkpoint,f.checkpoint AS finalized_checkpoint FROM "+s.internal("_ponder_go_chains")+" c JOIN "+s.internal("_ponder_go_progress")+" p USING(chain_id) JOIN "+s.internal("_ponder_go_finalized")+" f USING(chain_id)"); e != nil {
		return e
	}
	if e = tx.Exec(ctx, liveQueryNotifySQL(s.viewsSchema, s.viewsSchema+"_live_query")); e != nil {
		return e
	}
	for _, table := range []string{"_ponder_go_progress", "_ponder_go_finalized"} {
		if e = tx.Exec(ctx, "CREATE OR REPLACE TRIGGER "+schema.Quote(s.viewsSchema+"_live_query_notify")+" AFTER INSERT OR UPDATE OR DELETE ON "+s.internal(table)+" FOR EACH STATEMENT EXECUTE FUNCTION "+s.aliasTable("live_query_notify")+"()"); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) aliasView(ctx context.Context, tx Transaction, name, query string) error {
	if e := tx.Exec(ctx, "DROP VIEW IF EXISTS "+s.aliasTable(name)); e != nil {
		return e
	}
	return tx.Exec(ctx, "CREATE VIEW "+s.aliasTable(name)+" AS "+query)
}

// Initial safe metadata can seal historical records captured by the native
// journal. Default ordering removes only a globally safe prefix, never an
// interleaved operation behind an unknown/unsafe chain. Isolated is per-chain.
func (s *Store) pruneInitialSafePrefix(ctx context.Context, tx Transaction) error {
	if s.Ordering == schema.Isolated {
		return tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" j USING "+s.internal("_ponder_go_finalized")+" f WHERE j.chain_id=f.chain_id AND j.checkpoint<=f.safe_checkpoint")
	}
	r, e := tx.Query(ctx, "SELECT MIN(j.operation_id)::text AS id FROM "+s.internal("_ponder_go_journal")+" j LEFT JOIN "+s.internal("_ponder_go_finalized")+" f USING(chain_id) WHERE f.chain_id IS NULL OR j.checkpoint>f.safe_checkpoint")
	if e != nil {
		return e
	}
	if len(r.Rows) > 0 && r.Rows[0]["id"] != nil {
		unsafe, e := tx.Query(ctx, "SELECT j.operation_id FROM "+s.internal("_ponder_go_journal")+" j JOIN "+s.internal("_ponder_go_finalized")+" f USING(chain_id) WHERE j.operation_id>=$1 AND j.checkpoint<=f.safe_checkpoint LIMIT 1", r.Rows[0]["id"])
		if e != nil {
			return e
		}
		if len(unsafe.Rows) > 0 {
			return fmt.Errorf("initial safe checkpoints cross an unconfirmed global operation suffix")
		}
		return tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal")+" WHERE operation_id<$1", r.Rows[0]["id"])
	}
	return tx.Exec(ctx, "DELETE FROM "+s.internal("_ponder_go_journal"))
}
