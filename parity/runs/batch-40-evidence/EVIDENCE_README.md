# M41 frozen evidence

Created from `independent-storage-m41/full-verify-01` after all 65 stages,
12 explicitly targeted canonical collectors, and record-results passed.
This is a verified subset, not full project completion or publication evidence.

- Published parent: `1cdf7a50666ded90e5974de73a00cf3884e7c7fe`.
- Native inputs: 1,151 files, SHA256
  `48a34af5c9d4178fbb43024ba1b8d2ca1d1eb0db9c5970830db0b1ea1d84638a`;
  all 105 harness hashes remained unchanged throughout acceptance.
- Normal/race each passed 3,332 test nodes / 3,031 leaves, with zero failures or
  skips. Each mode ran 23 packages, 279 top-level functions, 85 serial batches.
- Formal coverage remains 543/905 (539 Go + 4 SDK), 362 pending, seven prior
  runtime-passed/static-pending cases unmapped. M41 adds zero original records,
  assertions, or type credit. All 543 previous mapping objects are byte-identical.
- `manifest.json` identifies 874 byte-checked copied files (175,662,948 bytes),
  6,016 validated current references, 313 explicitly historical references,
  1,467 dependencies, and 53 validated rebuildable exclusions. All 1,007 existing
  batch39 paths were checked unchanged before and after the copy.

The snapshot retains focused attempts/failures, real source lifecycle probes,
full native/source/type/SDK results, fixture receipts, exact collector logs,
ledger closeout, docs, and provenance. Failed or superseded observations are
preserved but are not current acceptance. Archived Go inputs use `.go.txt`, so
this archive cannot become an additional Go package.

The complete run used a fresh M41-owned PostgreSQL fixture, with independently
verified `SHOW data_directory` before legacy fixture DDL. Initial/final database
and role inventories contain only pristine defaults plus `ponder_test`; the
wrapper exited zero and stopped its server. The old task cluster was not reset
or maintained. The retained new fixture is separate from old-data maintenance.

Native PGlite has separate persistent business/sync directories and real SQL
business transactions; pinned source uses a same-instance callback-only QB
transaction. Source probes record that difference rather than claim equivalent
rollback. Their pending gate delays original insertion at wrapper admission,
not a source SQL-queue scheduling or commit-order proof. No outer runner, atomic
multichain setup/checkpoint, readiness, realtime or cutover completion is claimed.

Absolute execution roots and compiler hashes remain part of the evidence.
Dependency installations, rebuildable executables and authentication are not
published. Static audit is supported; relocated replay requires fresh pinned
preparation and new contexts. This README is an explanatory sidecar outside the
immutable manifest, matching the previous batch convention.
