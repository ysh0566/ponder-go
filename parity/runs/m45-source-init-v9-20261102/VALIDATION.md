# Managed live runtime through source-init-v9

This stage adds the managed live storage, recovery, cross-database coordination,
and RuntimeRunner lifecycle accumulated since M44. It supports default
multichain historical-to-live execution, private replay before publication,
continuous reorg recovery, cached and uncached acquisition, setup transactions,
and source-aligned initial checkpoints (including Down positions and start0).

The new constructors use independent versioned namespaces. Existing M44 databases
are not automatically migrated. Sync/cache and business storage remain separate
fault domains; reconciliation receipts coordinate their retries rather than
claiming a distributed transaction.

## Validated input and results

- Base commit: `1670566fb6efe03cab63f3da43a5f16b79a14cbb`.
- Frozen code/test input: **116 files**, snapshot fingerprint
  `acd0f26c08a055a15b10f81c91f314c50f7f7c6a7083c3b04f91637d71cd2cf9`.
- **530 unique native Go test leaves passed** on that one input:
  222 pure, 178 PostgreSQL, and 130 PGlite; zero native failures or skips.
- Full-package compile passed. The grouped command selections and file SHA256s
  are recorded in `commands.json`; they are a focused regression suite rather
  than a claim that every repository or original upstream test ran.
- Toolchain: Go1.25.0, Node24.22.1, PostgreSQL16.15, PGlite0.2.13.
- Pinned Ponder source:0.18.2,
  `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

Tests used isolated business/sync namespaces and dedicated native databases.
The PostgreSQL server ran as a nonroot task user on a local Unix socket.
Database services were stopped after each batch. Runtime process-exit tests
exercise application failure boundaries; they are not database engine,
server, or power-loss crash tests. PGlite results do not assert identical
durability semantics to PostgreSQL.

The v2 compatibility subgroup additionally used a retained fixture binary,
supplied through `PONDER_M45_RECOVERY_V2_TEST_BINARY`, with SHA256
`c80b82c29ced6f9ddb49573ec7cb87883a579e9c6b611549d647681b2d834df5`.
Build products, database directories, caches, credentials, and raw execution
logs are excluded from this commit.

## Compatibility boundaries and remaining work

The setup-only chain with no event filters preserves the pinned source's
Infinity-to-BigInt unsupported boundary. The startup RPC cache fence is
explicit hardening beyond the pinned startup behavior; it is not asserted
to have identical request/cache semantics.

Dynamic factory RuntimeRunner integration is a separate, uncompleted stage
and is excluded here. Nondefault ordering modes, CLI integration, full/race
acceptance, complete original upstream oracles, and the remaining one-to-one
test translations are still pending. Source mapping remains **543/905**;
these native test counts add no mapping credit. This is not full M45 acceptance.
