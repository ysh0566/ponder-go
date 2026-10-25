# Explicit loopback startup adaptation

This harness avoids **network-interface enumeration**, not networking itself. It still uses real loopback TCP, real Anvil, its official HTTP/WebSocket proxy, and real PostgreSQL/PGlite. No `os.networkInterfaces` replacement, fake interface, AF_UNIX fallback or platform permission change is used. It is **environment-adapted**, never a pristine original run. Source credit remains zero pending all independent parity obligations.

`loopback-adapter.mjs` exports `createAdaptedUpstream({artifactsFrom})`. The original checkout must be clean at commit `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. All 1,076 tracked files are copied and fingerprinted. Only `packages/core/src/_test/globalSetup.ts` changes: the official `createPool({autoPort:false})`, explicit proxy `127.0.0.1:8545`, and official function-valued Anvil options assigning `127.0.0.1:(18545 + worker ID)`. IDs outside 1–64 fail. Chain ID, mining mode, stop timeout, database cleanup, fixture hooks, input data and assertions remain unchanged.

Never share an adapted run's proxy port between concurrent suites. Up to two workers may run within a suite, with distinct explicit Anvil ports. Port collisions fail; they do not cause discovery or automatic fallback. Shutdown is the original official shutdown path. Separate process invocations may have distinct network namespaces, so all needed servers and clients must run in the same invocation.

Existing generated contract artifacts must come from an explicit checkout with every tracked input matching the pinned original. Their individual hashes and origin are recorded. Installed dependencies are linked read-only; sources are copied. The caller must retain `copy.manifest` and `copy.diff`, invoke `copy.verify()`, and always invoke `copy.dispose()` in `finally`. The manifest validator additionally recomputes the exact permitted startup transformation and rejects any changed business source, missing source, or changed generated artifact.

Commands (using the task's prepared environment and task-owned PostgreSQL only):

- `PONDER_ADAPTER_ARTIFACTS=/verified/artifact/checkout node --test tools/loopback-adapter.test.mjs`
- `PONDER_ADAPTER_ARTIFACTS=/verified/artifact/checkout node tools/loopback-adapter-check.mjs`
- `PONDER_ADAPTER_ARTIFACTS=/verified/artifact/checkout PONDER_ADAPTER_BACKEND=pglite node tools/loopback-adapter-source.mjs`
- With task PostgreSQL running in the same invocation and `PONDER_TEST_POSTGRES` supplied, use `PONDER_ADAPTER_BACKEND=postgres` for the source runner.
- `node tools/loopback-adapter-native.mjs` runs the existing four Go live differential tests sequentially (`-p=1`) using an explicit verified `PONDER_UPSTREAM_ROOT`. It requires the dedicated task PostgreSQL port 55432. Existing default paths and their original port 53398 remain unchanged when adapted mode is absent.

Every command writes a new evidence directory under `parity/runs/loopback-*`; failed evidence is retained. `PONDER_ADAPTER_REPORT` optionally selects a new directory and will not overwrite an existing one. Full source runs require 147 passes, zero failures and the one existing skipped declaration. This is distinct from native Go comparison success, and both are distinct from complete source-record credit. The startup probe is supplemental: it validates two-instance isolation, stable worker identity, snapshot/revert/replacement-block mechanics, WebSocket transport, stop/restart, collision failure/recovery and listener cleanup. It is not a replacement for original reorg tests.

The tsx CLI's separate AF_UNIX IPC issue is not changed by this work. Do not treat `node --import tsx` as guaranteed IPC-free; the pinned loader also imports an IPC client. That remains a separate scoped decision.
