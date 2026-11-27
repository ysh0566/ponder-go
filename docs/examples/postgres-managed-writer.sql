-- Run as deploy_manager between writers on the prepared managed-blocks deployment.
-- The administrator grants index_writer to deploy_manager, never the reverse.
-- This example uses the business schema ponder_go_blocks, its cache, and an
-- optional managed views schema ponder_go_blocks_views.
BEGIN;
SET LOCAL search_path TO pg_catalog;

ALTER SCHEMA ponder_go_blocks OWNER TO deploy_manager;
ALTER SCHEMA ponder_go_blocks_sync OWNER TO deploy_manager;
ALTER SCHEMA ponder_go_blocks_cache OWNER TO index_cache_owner;
ALTER SCHEMA ponder_go_blocks_cache_sync OWNER TO index_cache_owner;
ALTER SCHEMA ponder_go_blocks_cache_rpc_1 OWNER TO index_cache_owner;

-- Transfer only the current cache tables and their dependent sequences and
-- indexes. The no-login owner has authority only in this deployment's cache.
DO $handoff$
DECLARE
  item record;
BEGIN
  FOR item IN
    SELECT scope, name FROM (VALUES
      ('ponder_go_blocks_cache', ARRAY['_ponder_go_meta','_ponder_go_journal','_ponder_go_chains',
        '_ponder_go_events','_ponder_go_progress','_ponder_go_runtime_cursor','_ponder_go_finalized',
        '_ponder_go_domains_head','_ponder_go_domains_log']),
      ('ponder_go_blocks_cache_sync', ARRAY['_ponder_go_meta','blocks','intervals','rpc_cache','factory_addresses']),
      ('ponder_go_blocks_cache_rpc_1', ARRAY['blocks','factories','factory_addresses','intervals','logs',
        'rpc_request_results','traces','transaction_receipts','transactions'])
    ) AS objects(scope, names) CROSS JOIN LATERAL unnest(names) AS tables(name)
  LOOP
    EXECUTE format('ALTER TABLE %I.%I OWNER TO index_cache_owner', item.scope, item.name);
  END LOOP;
  ALTER FUNCTION ponder_go_blocks_cache._ponder_go_guard() OWNER TO index_cache_owner;
  ALTER FUNCTION ponder_go_blocks_cache._ponder_go_capture() OWNER TO index_cache_owner;
  IF EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'ponder_go_blocks_views'
             AND obj_description(oid, 'pg_namespace') = 'ponder-go:views:v1:ponder_go_blocks') THEN
    ALTER SCHEMA ponder_go_blocks_views OWNER TO deploy_manager;
    GRANT USAGE, CREATE ON SCHEMA ponder_go_blocks_views TO index_writer;
  END IF;
END
$handoff$;

GRANT USAGE, CREATE ON SCHEMA ponder_go_blocks TO index_writer;
GRANT USAGE ON SCHEMA ponder_go_blocks_sync, ponder_go_blocks_cache,
  ponder_go_blocks_cache_sync, ponder_go_blocks_cache_rpc_1 TO index_writer;
GRANT SELECT ON ponder_go_blocks_cache._ponder_go_meta,
  ponder_go_blocks_cache_sync._ponder_go_meta TO index_writer;
GRANT SELECT, UPDATE (sequence, hash) ON ponder_go_blocks_cache._ponder_go_domains_head TO index_writer;
GRANT SELECT, INSERT ON ponder_go_blocks_cache._ponder_go_domains_log TO index_writer;
GRANT SELECT, INSERT, DELETE ON
  ponder_go_blocks_cache_rpc_1.blocks, ponder_go_blocks_cache_rpc_1.logs,
  ponder_go_blocks_cache_rpc_1.transactions, ponder_go_blocks_cache_rpc_1.transaction_receipts,
  ponder_go_blocks_cache_rpc_1.traces, ponder_go_blocks_cache_rpc_1.factory_addresses,
  ponder_go_blocks_cache_rpc_1.intervals, ponder_go_blocks_cache_rpc_1.rpc_request_results TO index_writer;
GRANT SELECT, INSERT, UPDATE (fragment_id) ON ponder_go_blocks_cache_rpc_1.factories TO index_writer;
GRANT UPDATE (block_number) ON ponder_go_blocks_cache_rpc_1.factory_addresses TO index_writer;
GRANT UPDATE (blocks) ON ponder_go_blocks_cache_rpc_1.intervals TO index_writer;
GRANT UPDATE (result, block_number) ON ponder_go_blocks_cache_rpc_1.rpc_request_results TO index_writer;
GRANT USAGE ON SEQUENCE ponder_go_blocks_cache_rpc_1.factories_id_seq TO index_writer;
COMMIT;
