-- Run once as the owner of the prepared PostgreSQL deployment.
-- This example uses the ERC-20 account table, schema ponder_go_live and role index_writer.
BEGIN;

GRANT USAGE ON SCHEMA ponder_go_live, ponder_go_live_sync, ponder_go_live_rpc_1 TO index_writer;
GRANT SELECT ON ponder_go_live_sync._ponder_go_meta, ponder_go_live._ponder_go_chains TO index_writer;
GRANT SELECT, INSERT, DELETE ON ponder_go_live._ponder_go_journal, ponder_go_live._ponder_go_events TO index_writer;
GRANT SELECT, INSERT, UPDATE, DELETE ON
  ponder_go_live.account,
  ponder_go_live._ponder_go_runtime_cursor,
  ponder_go_live._ponder_go_progress,
  ponder_go_live._ponder_go_finalized,
  ponder_go_live_sync.blocks,
  ponder_go_live_sync.intervals,
  ponder_go_live_sync.factory_addresses,
  ponder_go_live_sync.rpc_cache
TO index_writer;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ponder_go_live_rpc_1 TO index_writer;
GRANT USAGE ON SEQUENCE ponder_go_live._ponder_go_journal_operation_id_seq TO index_writer;

-- The default multichain worker writes one metadata key during cross-chain rollback.
-- A separate SELECT policy lets it verify the deployment fingerprint on startup.
ALTER TABLE ponder_go_live._ponder_go_meta ENABLE ROW LEVEL SECURITY;
CREATE POLICY deployment_read ON ponder_go_live._ponder_go_meta
  FOR SELECT USING (true);
CREATE POLICY writer_replay ON ponder_go_live._ponder_go_meta
  TO index_writer USING (key = 'multichain_replay') WITH CHECK (key = 'multichain_replay');
GRANT SELECT, INSERT (key, value), UPDATE (value), DELETE ON ponder_go_live._ponder_go_meta TO index_writer;

COMMIT;
