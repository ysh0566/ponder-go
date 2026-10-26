# Finite default-multichain runner source probe (M43)

> Reconstruction notice (2026-10-26): the focused01–05 descriptions below
> report observations from the lost environment. Their raw run artifacts and PG
> directories are not recovered here and cannot serve as current acceptance.
> The restored implementation requires fresh `finite-historical-m43/rebuild-*`
> execution and proofs. Recovery/application provenance is recorded separately.

Pinned production source remains byte-identical. These supplemental, zero-credit
probes call `runMultichain` with two real Anvil chains, real RPC, SQL, extraction,
decoding and executable handlers. They never replace the coordinator or seed
business rows with expected outputs.

The real historical iterator is wrapped only at the consumer boundary. The next
pull is outside the business transaction. On a selected prefix or real iterator
exhaustion, a separately branded `NonRetryableUserError` stops before readiness,
indexes, views, triggers or realtime. All admitted RPC/cache work is drained.

Observations cover multiple groups, a second invocation using actual durable
checkpoints (without setup replay), empty completed-chain replay, column access
refetch inside the failed transaction, sticky error precedence, nonretryable
failure, real profile learning and predictive RPC prefetch. The prefetch wrappers
record calls but delegate unchanged; every yielded group has exactly one row and
RPC prefetch outside the transaction, even when a transaction callback retries.

Source PostgreSQL retries the entire transaction after `InvalidEventAccessError`.
Source PGlite invokes its transaction callback once, without SQL transaction or
automatic retry. Native persistent independent PGlite business/sync storage and
real SQL rollback remain an approved safety difference, not parity with this
source failure behavior. The source probes do not establish native close/fencing
or commit-outcome reconciliation; those require separate real native tests.

The existing `learned-prefetch` case additionally observes actual source
text-primary-key profile recovery (`block.number = 4n` -> `{id: 4n}` -> cache key
`"4"`), exact hint reuse/mismatch and the real PgText driver's identity mapping.
Its unchanged two-chain 240-event handler finds text IDs derived from block
numbers; its explicit 1-byte cache budget forces row-prefetch attempts.
PostgreSQL must complete all 240 events and successful prefetch. PGlite 0.2.13's
TEXT serializer rejects the predicted bigint before executing that SQL. The
source's unchanged query builder naturally performs ten attempts and nine waits
(125 through 32,000 ms), then rethrows its first generic `Error` with message
`Invalid input for string type`. This is an **expected upstream error**, not
pressure completion. The private raw first-error reference must equal both the
row-prefetch rejection and final runtime error. Every warning is observed through
an unchanged delegated logger; later attempts may create distinct Error objects.
No shutdown, retry alteration, or timeout is used to manufacture this terminal.
The actual business balances/cursors must equal only the dynamically observed
confirmed group prefix; the failed group must never enter a business transaction.

The separate `learned-prefetch-default` case retains the same 240 events, handlers,
RPC, and `syncEventsQuerySize=0` stress configuration, but preserves the actual
**default cache budget**, not all default runtime options. The captured budget
comes from source `buildOptions`, with `PONDER_CACHE_BYTES` absent, and is checked
against the actual V8 heap-size formula. Both backends must complete every block
1..120 exactly once per chain, learn includes/profile patterns and perform real
RPC prefetch. Complete row caches produce zero row-prefetch predictions, so this
case does not claim SQL row-prefetch coverage. `rowPrefetchPredictions` counts
predicted rows before query execution; it is not a successful SQL query count.
The stronger native internal recovered-text-key compatibility does not relax
public text CRUD or imply that the fixed source PGlite pressure input completed.

Include-array identity is observed directly while the live source objects still
exist: refetch observers run after the real missing-field reset and compare both
filters' `include` references with each other and with their original narrow
arrays. The learned-resolution observer compares each final reference with its
original and the other chain's include array. Serialized contents are never used
to infer sharing or replacement identity.

The learning contract is deployed by an actual fixture transaction on each chain.
Its receipt, bytecode and `eth_call` response are checked at block 1 before the
historical run reads that same height. Earlier failed probes at address `0x100`
are retained: the separate Anvil contrast observes identical stored bytecode but
different execution at that special address versus an actual deployment. The
contrast alone is not claimed to identify a particular precompile implementation.

The first successful-prefix probe also calls the unchanged `decodeEvents` and
both actual profilers on 11 supplemental ABI shapes. `Object.keys` is observed
on the live decoded objects before serialization: indexed-first insertion,
non-indexed insertion, numeric property-name ordering and boundaries, duplicate
name replacement without moving insertion position, named tuple results, and
array argument/result skipping. Source has no `FieldOrder` override API; native
explicit override tests are a separate configuration-boundary obligation. None
of these supplemental ABI observations creates original-test credit.

Only these new supplemental learning cases have explicit budgets: PostgreSQL
pressure and both default-cache variants use 60 seconds; the PGlite expected-error
regression uses 90 seconds so the unchanged 63.875-second backoff can naturally
terminate. The prior 15- and 60-second failed pressure captures remain immutable.
All 240 events and real RPC calls remain configured. Every original upstream test
and the other six supplemental timeouts are unchanged. Eight passing supplemental
test cases per backend do **not** mean eight successful runner completions: the
canonical `source_scopes` records PGlite `pressure_completed:false`,
`pressure_outcome:expected_upstream_error`, `source_error_observed:true`,
`full_backoff_terminal_observed:true`, and `diagnostic_abort:false`, independently
of `default_completed:true`. Validators and mutations reject false completion.

Capture scratch is not evidence: live `createIndexing` arguments (including the
actual circular metrics registry) stay private to an observer. Only explicit
count/pattern snapshots enter reports, including a `finally` snapshot. Original
runtime errors remain separate from later snapshot, drain, cleanup and reporter
errors. Names, constructors, messages, stacks, status/code, causes and explicit
cycle-reference markers are retained; reporter errors cause a failing result
rather than a silent success. The fresh source case also exercises this observer
with an actual source registry/error and a deliberately leaked scratch negative
control. This is a supplemental diagnostic-serialization control, not a claim
that an actual business callback failed in that successful-prefix scenario.

## Observed source-only focused05 result

`parity/runs/finite-historical-m43/focused-05` completed all five source/control
stages successfully with 1,456 frozen inputs unchanged and the fresh PostgreSQL
fixture stopped normally. Both backends passed eight supplemental test cases;
this is zero new original-test credit, **not** PGlite pressure completion.

- PostgreSQL pressure: 240 events, 44 groups and 726 real RPC requests completed
  in 19.009 seconds. Its row-prefetch metric counted 117 predicted rows.
- PGlite pressure: the unchanged 1-byte/240-configured-event input naturally
  terminated after 69.987 seconds with the original generic `Error: Invalid input
  for string type`. Ten observed warnings carried retry counts 0..9, with nine
  unchanged waits of 125..32,000 ms (63.891 seconds between first and terminal
  warnings). The final error and rejected row-prefetch error were the actual first
  warning's Error object; shutdown was still false. Two groups committed 12
  events in total, leaving each shared balance at six and durable cursors at that
  confirmed prefix. The third group failed before entering a transaction. Its
  metric counted six predicted rows, not successful SQL. No timeout or diagnostic
  abort substituted for this natural terminal; `pressure_completed` is false.
- The separate actual-default-cache-budget case completed all 240 events,
  44 groups and 726 RPC requests on **both** backends in about 19 seconds. The
  captured source/V8-derived cache budget was 469,762,048 bytes, with no environment
  override. Both recorded zero row-prefetch predictions; neither claims SQL
  row-prefetch coverage in this default-cache variant.

The formal actual-capture learning/error controls passed 48/48, the focused
actual-capture controls passed 36/36, and the focused guards passed 69/69, without
skips. Native tests were **not rerun in focused05**: its 1,175 native input files
were individually identical to focused04, whose native results are explicitly
referenced. Earlier focused01–04 failures and their input archives remain intact.
This focused success is not the still-pending complete 73-stage/14-collector
acceptance run or authorization to publish the milestone.
