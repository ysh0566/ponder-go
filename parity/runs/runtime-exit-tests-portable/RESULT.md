# Portable runtime process-exit tests

Continuous, source, setup and factory crash tests now allocate their evidence directories directly below the canonical absolute `PONDER_M45_TEST_ROOT`. The omnichain crash tests use the same helper. The tests continue through their original child-process, durable-state and recovery assertions.

The reference run used the preceding revision and a fresh PostgreSQL 14.24 fixture on loopback. All 23 selected leaves reached and failed the former fixed-path validation under `/tmp`. The output is preserved in `original.jsonl` in the local run directory.

## Commands

Set `PONDER_TEST_POSTGRES` to the disposable PostgreSQL fixture and both `PONDER_M44_TEST_ROOT` and `PONDER_M45_TEST_ROOT` to a fresh absolute task directory for each invocation. Use Go 1.25.11.

```sh
go test -count=1 -json ./historical \
  -run '^TestRuntime(Setup|Source|Continuous|FactoryTrace|FactoryTransaction|FactoryTransfer)Crash$'
go test -count=1 -json ./historical -run '^TestRuntimeOmnichain.*ApplicationExit$'

go test -race -count=1 -json ./historical \
  -run '^TestRuntime(Setup|Source|Continuous|FactoryTrace|FactoryTransaction|FactoryTransfer)Crash$'
go test -race -count=1 -json ./historical \
  -run '^TestRuntimeFactoryTransactionCrash$/^postgres$/^cache_false$/^(business|ack)$'
go test -race -count=1 -json ./historical -run '^TestRuntimeFactoryTransferCrash$'
go test -race -count=1 -json ./historical -run '^TestRuntime(Setup|Source)Crash$'
go test -race -count=1 -json ./historical -run '^TestRuntimeOmnichainLogOnlyApplicationExit$'
go test -race -count=1 -json ./historical -run '^TestRuntimeOmnichain(StaticLogs)?ApplicationExit$'
```

## Results

The same 36 PostgreSQL application-exit leaves passed in normal and race mode. Both modes executed every selected leaf. The initial combined race command passed 13 cases before reaching Go's 10-minute package deadline. Smaller invocations completed the rest using the same deadline.

The disposable PostgreSQL container and temporary evidence directories were removed. Cleanup also removed 134 MiB of generated configuration compiler outputs.

## Supporting test runs

- `go test -p 2 -count=1 -json ./abi ./bloom ./checkpoint ./events ./factory ./finality ./interval ./query ./rpc ./schema ./sqlquery` passed 633 leaves. The same command with `-race` passed the same set. The retained client compatibility cases passed after installing the pinned client and React dependencies with the upstream lockfile.
- `go test -count=1 -json ./filter` passed 935 leaves. `go test -p 1 -count=1 -json ./config ./orm` passed 70. Both selections also passed with `-race`. The configuration compiler's original input was restored using `tools/parity_archive.py` and matched its recorded SHA-256.
- `go test -count=1 -json ./runtime -run '^(TestAnvil(Postgres|PGlite)Vertical|TestStagedAnvilVertical|TestContractLogAndTraceBatch|TestContractTraceRuntime|TestFactoryRuntime|TestSourceTraceIdentityVectors)$'` passed 28 leaves in each mode, with `-race` added for the second run.
- The pinned upstream core selection in `tools/verify.sh` passed 201 cases and retained two source skips. The SDK client passed three tests and the SDK React type check passed one test with TypeScript 5.4.5.
- Foundry 1.8.5 and Solidity 0.8.37 were verified against official SHA-256 values and used from the task workspace. The upstream generated contract artifacts came from `wagmi generate`.
- The archive verifier checked 34,319 archived paths and 9,227 unique blobs against the retained source commit `fe9f4729693c8040340656c6ce49dd2a788ad999`. Only the configuration input needed for execution was restored.
