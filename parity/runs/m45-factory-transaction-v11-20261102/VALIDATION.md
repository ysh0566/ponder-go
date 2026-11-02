# Managed factory account transactions

Opt-in FactorySources + SourceCheckpoints + Live now accepts account factory
transaction:from and transaction:to handlers in default multichain RuntimeRunner.
The same existing historical acquisition, SQL, event builder and managed
transaction/recovery paths execute the business handlers on PostgreSQL and PGlite,
with either RPC cache policy. Factory trace/transfer filters remain rejected.

The pinned oracle executes actual createRealtimeSync, createSyncStore and SQL
from Ponder 17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334. It establishes
birth-block membership (even before the creation transaction), persistent child
membership after discovery ends, reverted receipt exclusion, null-to behavior
and canonical membership after reorg. Provider fixtures are deep snapshots taken
before the upstream synchronizer mutates transaction arrays; receipt status is
bound to immutable transactionIndex. Original erroneous fixtures and failures
are retained in the external evidence directory.

SQL returns int64 transaction/log indices. The event numeric conversion now
accepts that exact type, preserving receipt pairing and full checkpoint indices.
Previously its unchecked numeric caller treated these SQL indices as zero.
Focused transaction, log and trace index regressions protect this fix.

The native matrix compares business SQL rows and complete handler checkpoints
to the source oracle: historical/live handoff, child discovered before business
start, reopen, global-suffix reorg, failed handler after SQL write, and PostgreSQL
application exit at prepare/business/ack with both cache policies. The latter
keeps the database server running; it is not an engine-crash test.

Validation selection: 522 distinct native test leaves
(409 pure, 78 PostgreSQL, 35 PGlite),
plus compile-only all-package checking and focused race repeats. Repeats add no
unique test credit. Exact commands and expected native names are in commands.json;
actual group receipts, archives, modes, patches, logs, review, postcheck and seal
are retained at /root/repo/ponder-go/m45-factory-extended-z4-20261102T193052Z-o71nm_um.
Every native run must match the final frozen input; earlier smoke passes are not
transferred. This is a selected affected regression closure, not full repository
or full race acceptance.

Runtime account registrations expose separate from/to callbacks. A single
dual-factory-endpoint transaction:both filter is source-oracle/event-builder
evidence, not an added RuntimeRunner registration. Full original upstream oracle,
other runtime modes, CLI, PGlite abrupt application exit, and database-engine,
server and power-loss crash acceptance remain pending. Prefix reconstruction
still requires archival RPC, even with a warm cache. No existing database is
migrated. M45 remains unaccepted and original-source mapping stays 543/905.

Base: a7cbf53b4ff7d79f1def47fceb88634b8ac1ec23.
Late parallel source-init commit b644bc7 has the identical tree to c2eb45a;
late factory ea5913b differs from a7cbf53 only in the previous stage's validation
documents. This development line remains a descendant of a7cbf53. main is unchanged.
