# Versioned cache in the native event path

`Store.EnableProtocolCache` attaches the owned nine-table schema created from
the exact fixed-source migration SQL. The native single-chain engine enables it
by default before sync. `runtime.NewWithOptions(..., Options{DisableCache:true})`
runs without RPC-result caching and without protocol-table writes. This is a
native API; the complete upstream cache/configuration surface is unfinished.

Complete raw RPC block JSON is retained instead of discarding fields not used by
the handler model. SaveBlock and CompleteBlock write normalized blocks and full
transactions in the same transaction as native sync provenance. Contract/factory
log requests write logs; requested transaction receipts write receipt rows.
Completion mirrors native filter IDs to source multiranges. Factory discoveries
mirror native factory IDs/addresses to the versioned relations. Native IDs are
not claimed to equal the source fragment algorithm.

`ProtocolCache.Insert` supports source-shaped block, log, transaction, receipt
and trace rows, preserving exact hex-to-decimal quantities, nullable fields,
address normalization, trace addresses and conflict-do-nothing behavior. A batch
is atomic. Trace ingestion is available here; automatic RPC trace collection and
trace event dispatch remain unimplemented. `Rows` supports bounded table reads,
not the full source filter/include/pagination planner.

RevertAndRewind clears the attached cache's orphaned rows and clips intervals in
the same transaction as native business undo and provenance rewind. A failure
at a cache DELETE must roll back business undo too. Generic native RPC request
cache entries use a chain-scoped SHA256 request key and nullable block metadata;
rewind conservatively clears unknown-block results for the affected chain.
This differs from the source MD5/ordered-object request-key algorithm and does
not promise reuse of existing TS client cache entries. Closed adapters reject
reads/writes; backend lifecycle and lease fencing still govern writes. A cold or
disabled runtime discovers an existing owned/complete schema inside its rewind
transaction and invalidates it without enabling caching or creating a schema.
Native factory IDs include chain ID before entering the source global factory
identity relation, preserving the native API's per-chain key separation.

Twenty generated vectors evaluate the unmodified fixed-source encode.ts and
lowercase.ts functions using installed dependencies. Their hashes and exact
outputs are stored in `testdata/protocol/encoding.json`. They are supplemental
differential evidence, not new upstream test mappings. The full-RPC integration
uses the existing local Anvil and real PostgreSQL/PGlite separately, including
repeat sync and a fork replacement; it does not establish Foundry 1.8.5 oracle
compatibility.

Still required: original source CRUD/fragment/filter cases with their exact
Foundry fixtures; full request hash
and block-number pruning protocol; query projections/pagination; automatic trace
and account/native-transfer sources; staged/multichain scheduling and full
history-to-realtime handoff. Old DB takeover remains a separate track.

Cold-cache regression tests close/reopen real PG connections and disk-backed
PGlite, invalidate all five event relations plus factory addresses/RPC results,
preserve another chain, and re-enable/replay. A real cache DELETE trigger error
proves that cold invalidation and business undo/provenance roll back together.
Disabled rewinds leave an absent cache schema absent and refuse an unowned
colliding schema. The original red run is retained separately. These are native
regressions, not additional source mappings. Re-enabling does not backfill cached
logs/receipts for an uncached period; the complete cache planner must use missing
intervals to fetch those data in a later milestone.
