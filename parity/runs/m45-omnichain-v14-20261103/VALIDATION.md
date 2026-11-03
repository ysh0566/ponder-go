# M45 v14: bounded finalized omnichain

Base: `510cb73e2791b07cb6f3342a2048a09eb98b6b36`. Fixed Ponder 0.18.2:
`17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`.

This stage enables actual RuntimeRunner omnichain execution for bounded,
globally finalized ranges with cache enabled and one interval1/offset0 block
callback per chain, startBlock >= 1. Live/pending/reorg, factories, non-block
callbacks, cache disabled, setup and start0 remain rejected before storage opens.
The published default multichain paths remain supported.

The runner calls the existing `HistoricalSourceOmnichain` SQL paging and merge
coordinator. Each complete Groups output is one business transaction, with
globally sorted block events. It preserves the exact group checkpoint: a
completed block callback can leave a source event checkpoint rather than
block-Up. The v11 persisted runtime format retains physical header identities
separately from logical source Down/event/Up positions. Older formats retain
their original position rules and reject the new wire fields, including empty,
null and case aliases in nested receipts.

Every successful historical group commits business writes, source progress,
safe/finalized positions and journal cleanup together. The existing batch and
finalize planners/receipts share that one transaction. Public reads, retries and
commit reconciliation require the exact paired safe receipt; the writer's
internal pre-commit intermediate state grants no public success.

The independent oracle executes the pinned source's actual `runOmnichain`,
`buildSchema(ordering=omnichain)` and `createDatabase`, backed by PGlite and a
controlled RPC provider. Native fixtures preserve its raw blocks, handler
order/ids, SQL rows and checkpoints. No hand-inserted business/storage input
substitutes for RuntimeRunner acquisition.

- Oracle01: interleaved timestamps, a chain-ID tie and cold reopen without handler replay.
- Oracle02: local finality alone leaves source pending business; native bounded preflight rejects a range beyond the global watermark.
- Oracle04: default query size12000; the first3 events commit, event4 fails in a separate merged transaction. SQL preserves the3-event prefix; chain2 retains the block1 event checkpoint. Cold reopen runs only event4.
- Oracle05: the same default configuration and input; event3 fails, rolling back its entire merged transaction to empty SQL and initial Down checkpoints. Cold reopen runs all4 events.
- Oracle06: distinct starts/ends; an unexecuted later Down baseline does not block an earlier event from another chain. Cold reopen runs no handlers.
- Oracle03 retains the earlier query-size6 diagnostic. Its result is not substituted for the default-configuration oracle05.

Development failures remain in the external evidence directory. In particular,
the original entry-rejection red test, build/setup failures and
`red-omni-handler-01` are retained. That red test proved the first implementation
incorrectly left a2-event prefix after event3 failed; the source merged-group
transaction path repairs that behavior. Preliminary passes from older inputs
are not acceptance credit for the final frozen input.

The selected final plan has132 groups,669 unique qualified leaves
(456pure/144PG/69PGlite),276 race executions and945 total case executions.
It reruns all612 v13 selected leaves;57 additional leaves are36 omnichain/receipt
supplements and21 legacy store regression controls. One old native preflight
case now rejects omnichain because its end exceeds acquired finality; isolated
ordering still fails its unsupported-mode gate. Its refusal-before-storage
assertion remains intact.

Commands and exact selections are in [commands.json](commands.json). Full input
archives, patches/modes, native JSON logs, source probes/locks, crash children,
dispatch receipts, independent audits, POSTCHECK and checksums are retained at:

`/root/repo/ponder-go/m45-omnichain-z4-20261103T070052Z-rv72df6n`

Final publication requires the entire same-input plan and two independent reviews
to pass, exact remote SHA verification, and the visible GitHub CI record. A zero
check/context count is recorded as no visible CI, not green CI. No force push,
main overwrite or branch-protection bypass is permitted.

This selected stage is not full repository/full-race or M45 acceptance.
Formal mapping stays543/905; supplemental tests and repeated race executions
add no original-source credit. See [module support](../../../docs/RUNTIME_SUPPORT.md).
