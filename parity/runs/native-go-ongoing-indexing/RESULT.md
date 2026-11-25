# Continuous Go starter audit

The audit used the public Go starter entry point at `20b3201d912131e8909dfdd0822c878ce5320e8a`, with Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13. Ponder 0.18.2 is pinned at `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

## Finality and journal retention

One local Anvil instance mined three blocks. For each case the probe opened a fresh atomic Store, called `runtime.NewIndexer` and ran the worker with `RunIndexing`. Its block handler inserted the block number into a business table once per block. Both configurations used `StartBlock: 1`, `Interval: 1` and an explicit zero-second reorg window. The test stopped the continuous worker at readiness and let the bounded worker complete.

| Backend | EndBlock | Business rows | Latest indexed block | Finalized block | Undo records remaining |
| --- | --- | ---: | ---: | --- | ---: |
| PostgreSQL | Unset | 3 | 3 | No row | 3 |
| PostgreSQL | 3 | 3 | 3 | 3 | 0 |
| PGlite | Unset | 3 | 3 | No row | 3 |
| PGlite | 3 | 3 | 3 | 3 | 0 |

The atomic worker returns before calling `Store.Finalize` when its end block is unset. Staged lifecycle workers have a separate finalization path. To reproduce the measurements, run these queries in the new indexing namespace after the worker reaches readiness.

```sql
SELECT COUNT(*) FROM seen;
SELECT chain_id, checkpoint FROM _ponder_go_progress;
SELECT chain_id, checkpoint FROM _ponder_go_finalized;
SELECT COUNT(*) FROM _ponder_go_journal;
```

All four observations completed. With the test database and Anvil and PGlite dependencies configured, run the checked-in probe from the repository root. The overlay makes it available only for this invocation.

```sh
overlay=$(mktemp)
python3 - "$overlay" <<'PYTHON'
import json, sys
from pathlib import Path
root = Path.cwd()
source = root / "parity/runs/native-go-ongoing-indexing/probe_test.go.txt"
target = root / "runtime/audit_ongoing_native_test.go"
Path(sys.argv[1]).write_text(json.dumps({"Replace": {str(target): str(source)}}))
PYTHON
go test -count=1 -overlay "$overlay" -run '^TestAuditContinuousFinality$' -v ./runtime
```

[Go runtime at the audited commit](https://github.com/ysh0566/ponder-go/blob/20b3201d912131e8909dfdd0822c878ce5320e8a/runtime/engine.go#L305-L326)

## Block notifications

The native starter fetches the latest block over RPC and waits on a timer between scans. The RPC package separately implements WebSocket subscriptions and reconnects. The pinned upstream runtime invokes its RPC subscription when it begins realtime indexing. [Go polling loop](https://github.com/ysh0566/ponder-go/blob/20b3201d912131e8909dfdd0822c878ce5320e8a/runtime/engine.go#L621-L655) · [Go subscription](../../../rpc/client.go) · [Upstream realtime startup](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/runtime/realtime.ts#L814)

`go test -count=1 -run '^(TestSubscriptionReconnect|TestSubscriptionPermanentPolicyClose)$' ./rpc` and the same command with `-race` exercise the low-level subscription.

## PostgreSQL column types

The pinned public exports include `cidr`, `inet`, `interval`, `macaddr`, `macaddr8` and `time`. A Go program calling `schema.SQLType` on each returned its unsupported-column error for all six. The same program resolved `point` and `line` to their PostgreSQL types. Earlier backend and GraphQL checks cover those two types. [Public upstream types](https://github.com/ponder-sh/ponder/blob/17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334/packages/core/src/index.ts#L77-L109) · [Go schema](../../../schema/schema.go) · [Earlier checks](../native-go-scope-audit/RESULT.md)
