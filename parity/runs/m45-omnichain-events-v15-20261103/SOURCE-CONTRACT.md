# v15 source contract

The fixed upstream is17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334(Ponder0.18.2).
The independent oracle uses267 unchanged source files verified against Git blobs,
locked dependencies, actual schema/database construction and runOmnichain.

runtime/historical.ts#getLocalEventGenerator reads sync-store SQL pages by block
range. getHistoricalEventsOmnichain decodes raw logs through ABI callbacks and
filters crash positions. mergeAsyncGeneratorsWithEventOrder partitions at the
minimum page checkpoint, preserving all events at the chosen physical block
boundary. runtime/omnichain.ts zips the complete returned chain groups and commits
business writes and every returned chain checkpoint in one userQB transaction.

The38-call fixture proves ascending timestamp/chain ordering, the two source-order
callbacks on one log, multiple logs per block, a block without logs, explicit SQL
pagination, exact whole-merged-output rollback and cold continuation. Source09
and10 fail at the same handler after a shared write but retain20 and0 committed
calls respectively; native assertions use those raw SQL/checkpoint snapshots.

One closing block callback per chain is a structural requirement for this native
extension. It covers every log range, leaves exactly one canonical closing event
in each complete block partition, and preserves v11's strict position grammar.
Removing that callback is a separate dependency: the source can omit empty event
groups and leave a chain at an earlier event/Down position. Supporting log-only
or other event sources requires deriving and preserving those source positions,
sparse/empty ranges and activation semantics; merely accepting arbitrary logical
checkpoints in the old durable format is insufficient.

Production entry accepts only closing block plus non-factory contract logs for
this ordering; realtime/pending/reorg and other callback kinds remain rejected.
The generic existing ABI/RPC/sync-store machinery is reused, while this stage's
direct integration observations are the named static nonindexed uint256 fixture,
two overlapping callbacks, two chain timelines and the documented failure points.
Other ABI/provider/source combinations retain their existing module-level
coverage and are not a complete omnichain compatibility claim.
