# Managed live log factory runtime

This stage connects opt-in `RuntimeOptions.FactorySources` to the actual default
multichain historical/live RuntimeRunner. It requires SourceCheckpoints and Live,
and uses the separate `managed-live-factory-v10` namespace. Existing namespaces
and M44 databases are not migrated.

Factory creation logs discover child addresses by birth block. Child events in
the same block can match even before the creation log; factory end bounds stop
discovery, while previously discovered children remain active. Reopen, handoff,
retry, and reorg reconstruct authentic ancestry and factory membership. Shared
fragments register every matching factory. Transport errors retry; invalid
block identity fails. Prefix reconstruction is linear in historical blocks and
requires an archival provider even with a warm cache.

## Validation

- Parent stage: source-init-v9, commit b644bc7191c31223ccdf8cc98a1418202b560a26.
- Frozen code/test input: 120 files, fingerprint
  `2229fa9d3d8dc29b72816458c8ad8e4a71629a0f8b44db4db4c7c2639a0c6edf`.
- 581 unique native test leaves passed on this input: 249 pure, 190 PostgreSQL,
  142 PGlite. This reruns all 530 prior leaves and adds 51, with zero native
  failures or test skips. Package-only no-test skips receive zero credit.
- All 134 groups have exact commands, leaf selections, source hashes, archive
  content/mode/patch checks, native exit codes, and stopped-process evidence.
  Compact commands and source hashes appear in commands.json.
- Two independent source and result reviews reported zero remaining findings.
- Go1.25.0, Node24.22.1, PostgreSQL16.15, PGlite0.2.13.
- Pinned upstream Ponder0.18.2:
  `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.
- A minimal oracle executed actual pinned exports: 38 observations, 35 returned
  results and three expected source errors. It is not the full upstream runtime
  or SQL sync-store oracle. Go golden assertions cross-check 22 selected
  observations; the remaining 16 observations are source-only evidence.

The same retained v2 fixture binary described in the previous stage was used
for compatibility regression. PostgreSQL ran as a nonroot user over a local
Unix socket; all database storage and task artifacts remained in the project
storage root. Test services stopped after each group. Failure/control logs and
source snapshots are retained separately, outside the publication.

## Scope and remaining work

Transaction, trace, and transfer factory filters are explicitly rejected by
this runtime entry. Factory-specific application process-exit fault tests,
extended refetch/wildcard/repeated-birth cases, full original runtime/SQL
oracles, other ordering modes, CLI, full/race, and DB engine/server/power-loss
crash acceptance remain pending. Existing application-exit regressions do not
claim database-engine durability equivalence; PGlite and PostgreSQL remain
distinct backends.

The setup-only Infinity-to-BigInt unsupported source boundary is preserved.
The startup RPC cache fence remains disclosed hardening beyond pinned behavior.
Source mapping stays 543/905; supplemental test counts add no mapping credit.
This is a scoped stage, not full M45 acceptance.
