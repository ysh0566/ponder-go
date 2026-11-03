# M45 v15 bounded omnichain static logs

The selected RuntimeRunner closure adds static contract ABI log callbacks beside
exactly one interval1/offset0 closing block callback per chain. The block callback
must cover every log callback range. All ranges start at >=1 and end below the
same acquired global finalized watermark. RPC caching is enabled.

Complete historical source Groups remain one business transaction across chains.
Physical blocks retain authenticated lineage; all log callbacks in a block run
before its one canonical closing block callback. Existing v11 Down/Up/block-event
positions and strict durable receipts are retained.

Historical.QuerySize accepts1..12000 for this ordering;0 uses the upstream default
12000. Other historical scheduling/observer options and nonzero multichain query
options remain rejected before RPC/storage. Live/pending/reorg, factory, log-only,
account transaction/transfer/function trace sources, cachefalse/setup/start0 are
outside this omnichain closure. This does not accept PGlite abrupt application
exit or database engine/server/power-loss failures.

## Pinned source observations

Fixed source: Ponder0.18.2 at17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334.
Five probes execute actual runOmnichain, buildSchema and createDatabase using
PGlite. They use two chains, three indexed blocks per chain, four logs in blocks1
and3, no logs in block2, and two overlapping ABI callbacks on each log. Their38
handler calls, ABI values, IDs/checkpoints, timestamps, hashes and SQL snapshots
are bound in testdata/runtime_omnichain_logs_source_golden.json.

| Actual source label | Query target | Controlled failure | Committed calls after failure | Cold recovery calls |
| --- | ---: | --- | ---: | ---: |
| omni-logs-source-05 |6|none|38|0|
| omni-logs-source-07 |12000|none|38|0|
| omni-logs-source-06 |6|1:3:A:1 after shared SQL write|29|9|
| omni-logs-source-09 |6|2:3:A:1 after shared SQL write|20|18|
| omni-logs-source-10 |12000|2:3:A:1 after shared SQL write|0|38|

The next cold open makes0 business calls in all cases. Small-query source SQL
actually reads [1,2] and [3,3]. The20 versus0 prefix proves the merged-output
transaction boundary; neither per-block commits nor one whole-task transaction
matches both observations.

The initial source01 fixture omitted required null topic wildcards, causing the
upstream to expand undefined topic slots and match no logs. Its failed receipt,
raw RPC, stdout/stderr and data remain preserved, with0 acceptance credit.
Corrected probes use the fixed LogFilter fields and contract metadata.
Unexecuted prototypes are not observations.

## Exact selected native plan

The plan is72 groups,559 unique qualified leaves:473 pure,64 PostgreSQL and22
PGlite. There are250 race executions and809 total case executions, with at most2
groups active. The31 new qualified supplements comprise17 pre-RPC/DDL capability
cases,10 source-golden business cases across PG/PGlite, and4 PG application-exit
cases. Each new case is included in the selected race plan.

Relative to v14's669 unique leaves,528 are selected again,141 are explicitly not
rerun on this snapshot, and31 are additional. This is a changed selection, not a
whole-project progress percentage. The complete omitted qualified set is in
commands.json baseline_comparison. Selected regression includes all v14 pure
selections, prior omnichain and legacy-format controls, and affected default
runtime source/initial/range/recovery cases. Other factory and runtime database
corpora retain their prior evidence and are not claimed revalidated here.

The dedicated PostgreSQL application exits use os.Exit83 during a log handler
after shared SQL writes and after activation, at both query settings. They check
the source's exact committed prefix, logical recovery positions, shared business
state, replay suffix, Ready and a further cold open. PostgreSQL stays running;
these are application process exits, not database-engine crash tests.

## Evidence and acceptance

Exact commands and qualified leaves are in commands.json. Native runners archive
the changed file contents/modes, full tracked patch, command, base commit/tree,
before/after fingerprint and raw Go JSON. The phase freezes one such input in
FROZEN-INPUT.json and FROZEN-INPUTS.tar.gz in the task-owned evidence directory.
Preliminary red/green inputs are retained as development history and do not
replace the final same-input selection.

Both old entry rejection and the old one-event-per-block rejection were retained
as real red executions before implementation. Final acceptance requires strict
raw Test/package terminal validation, exact559/250/809 counts, source/raw/golden
cross-checks, all child log/spec/marker checks, PG inventories/shutdown and actual
native/PGlite process cleanup, plus independent Spec and Standards code and
result reviews. Completed release evidence is recorded in FINAL-AUDIT.json,
POSTCHECK.json, PUBLISH-RECEIPT.json and FINAL-SEAL.json.

Formal source mapping remains543/905;362 static records remain unimplemented,
and7 runtime-passed/type-pending records are not credited complete. Source
probe observations, native supplements and race repeats add0 original-source
credit. Full repository/full-race and complete M45 compatibility remain open.

The first final attempt stopped FAILED after53 groups because the new capability
validator changed two retained diagnostic substrings (startBlock>=1 and
interval1 block callback). Both unsafe requests still failed before storage or
callbacks. The implementation restores those messages; original guard assertions
are unchanged. Original receipts/logs/archives and the failed input are preserved.
A fresh fingerprint and fresh task labels bind the complete second plan; none of
the first attempt's results counts as passing the new snapshot.
