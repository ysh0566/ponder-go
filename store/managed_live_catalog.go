package store

import (
	"context"
	"fmt"
)

// Catalog coverage includes base and internal columns/defaults, constraints,
// indexes, all triggers and function bodies, sequences' definitions and enums.
// Physical node expressions avoid search_path-dependent pretty-printer output.
// Sequence values and heartbeat/lock timestamps are intentionally not identity:
// PostgreSQL sequences are not transactional and Close/heartbeat may update them.
func (m *ManagedLive) catalogHash(ctx context.Context, q Querier) (string, error) {
	if m.opts.liveRuntime {
		if err := q.Exec(ctx, "SET LOCAL search_path TO pg_catalog"); err != nil {
			return "", err
		}
	}
	s := m.owner.s
	queries := []string{
		`SELECT n.nspname,c.relname,c.oid::text,c.relkind::text,c.relpersistence::text,c.relowner::text,c.reloptions::text,c.relrowsecurity,c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname`,
		`SELECT n.nspname,c.relname,a.attnum::text,a.attname,a.atttypid::text,a.atttypmod::text,a.attcollation::text,a.attnotnull,a.attidentity::text,a.attgenerated::text,a.attstorage::text,COALESCE(d.adbin::text,'') AS default_expression FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname IN ($1,$2,$3) AND a.attnum>0 AND NOT a.attisdropped ORDER BY n.nspname,c.relname,a.attnum`,
		`SELECT n.nspname,c.relname,k.conname,k.contype::text,k.condeferrable,k.condeferred,k.convalidated,k.conkey::text,k.confkey::text,k.confrelid::text,k.confupdtype::text,k.confdeltype::text,k.confmatchtype::text,k.conbin::text FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname,k.conname`,
		`SELECT n.nspname,c.relname,i.indisunique,i.indisprimary,i.indisvalid,i.indisready,i.indnkeyatts::text,i.indkey::text,i.indclass::text,i.indcollation::text,i.indoption::text,i.indexprs::text,i.indpred::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname`,
		`SELECT n.nspname,c.relname,t.tgname,t.tgenabled::text,t.tgtype::text,t.tgfoid::text,t.tgparentid::text,t.tgisinternal,t.tgconstraint::text,t.tgconstrrelid::text,t.tgconstrindid::text,t.tgdeferrable,t.tginitdeferred,t.tgnargs::text,t.tgattr::text,encode(t.tgargs,'hex') AS arguments,t.tgqual::text,t.tgoldtable,t.tgnewtable FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname,t.tgname`,
		`SELECT n.nspname,p.proname,p.proargtypes::text,p.proallargtypes::text,p.proargmodes::text,p.prorettype::text,p.prolang::text,p.prokind::text,p.proisstrict,p.proretset,p.proparallel::text,p.proleakproof,p.provolatile::text,p.prosecdef,p.proconfig::text,p.prosrc,p.probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,p.proname,p.proargtypes::text`,
		`SELECT n.nspname,c.relname,q.seqtypid::text,q.seqstart::text,q.seqincrement::text,q.seqmax::text,q.seqmin::text,q.seqcache::text,q.seqcycle FROM pg_sequence q JOIN pg_class c ON c.oid=q.seqrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname`,
		`SELECT n.nspname,t.typname,e.enumsortorder::text,e.enumlabel FROM pg_enum e JOIN pg_type t ON t.oid=e.enumtypid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,t.typname,e.enumsortorder`,
		`SELECT n.nspname,c.relname,r.rulename,r.ev_type::text,r.is_instead,r.ev_qual::text,r.ev_action::text FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname,r.rulename`,
		`SELECT n.nspname,c.relname,p.polname,p.polcmd::text,p.polpermissive,p.polroles::text,p.polqual::text,p.polwithcheck::text FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ($1,$2,$3) ORDER BY n.nspname,c.relname,p.polname`,
	}
	all := make([]any, 0, len(queries))
	for _, query := range queries {
		r, err := q.Query(ctx, query, s.Namespace, s.SyncNamespace, m.opts.ViewsSchema)
		if err != nil {
			return "", err
		}
		all = append(all, r.Rows)
	}
	return managedHash(all), nil
}

// The state hash binds all currently supported durable state, not a >= latest
// shortcut. Excluding only the terminal receipt's own after_hash breaks the
// otherwise circular hash. Earlier receipts, including their hashes, stay bound.
func (m *ManagedLive) snapshotHash(ctx context.Context, q Querier, terminal string) (string, error) {
	s := m.owner.s
	catalog, err := m.catalogHash(ctx, q)
	if err != nil {
		return "", err
	}
	all := []any{catalog}
	queries := []struct {
		sql  string
		args []any
	}{
		{"SELECT key,identity,lease_mode FROM " + s.internal("_ponder_go_managed_live_identity") + " ORDER BY key", nil},
		{"SELECT key,value FROM " + s.internal("_ponder_go_meta") + " ORDER BY key", nil},
		{"SELECT key,value FROM " + s.syncTable("_ponder_go_meta") + " ORDER BY key", nil},
		{"SELECT payload::text FROM " + s.internal("_ponder_go_live_control") + " ORDER BY key", nil},
		{"SELECT r.id,r.generation::text,r.owner_token,r.kind,r.request_hash,r.previous_operation,r.before_hash,CASE WHEN r.id=$1 THEN '' ELSE r.after_hash END AS after_hash,r.payload::text FROM " + s.internal("_ponder_go_live_operations") + " r ORDER BY r.generation,r.id", []any{terminal}},
		{"SELECT r.ordinal::text,r.payload::text FROM " + s.internal("_ponder_go_live_segments") + " r ORDER BY r.ordinal", nil},
		{"SELECT r.chain_id::text,r.block_number::text,r.block_hash,r.payload::text FROM " + s.internal("_ponder_go_live_proofs") + " r ORDER BY r.chain_id,r.block_number,r.block_hash", nil},
		{"SELECT (value-'heartbeat_at'-'is_locked')::text AS value,lease_token FROM " + s.internal("_ponder_go_app") + " ORDER BY key", nil},
		{"SELECT r.chain_id::text,r.chain_name FROM " + s.internal("_ponder_go_chains") + " r ORDER BY r.chain_id", nil},
		{"SELECT r.chain_id::text,r.checkpoint FROM " + s.internal("_ponder_go_progress") + " r ORDER BY r.chain_id", nil},
		{"SELECT r.chain_id::text,r.checkpoint,r.safe_checkpoint FROM " + s.internal("_ponder_go_finalized") + " r ORDER BY r.chain_id", nil},
		{"SELECT r.chain_id::text,r.checkpoint FROM " + s.internal("_ponder_go_runtime_cursor") + " r ORDER BY r.chain_id", nil},
		{"SELECT r.operation_id::text,r.table_name,r.row_key::text,r.before_row::text,r.row_data::text,r.operation::text,r.checkpoint,r.chain_id::text FROM " + s.internal("_ponder_go_journal") + " r ORDER BY r.operation_id", nil},
		{"SELECT event_id,checkpoint,chain_id::text,operation_boundary::text,operation_end::text FROM " + s.internal("_ponder_go_events") + " ORDER BY event_id", nil},
		{"SELECT name,owner_token,catalog_hash FROM " + s.internal("_ponder_go_managed_stages") + " ORDER BY name", nil},
	}
	for _, query := range queries {
		r, err := q.Query(ctx, query.sql, query.args...)
		if err != nil {
			return "", err
		}
		all = append(all, r.Rows)
	}
	for _, table := range s.Schema.Tables {
		r, err := q.Query(ctx, "SELECT to_jsonb(t)::text AS row FROM "+s.table(table.Name)+" t ORDER BY to_jsonb(t)::text")
		if err != nil {
			return "", fmt.Errorf("read managed live durable business rows: %w", err)
		}
		all = append(all, r.Rows)
	}
	return managedHash(all), nil
}
