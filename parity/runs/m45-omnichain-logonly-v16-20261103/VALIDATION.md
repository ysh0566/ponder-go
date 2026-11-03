# v16 selected validation: uniform log-only omnichain

Finite globally finalized static ABI log-only callbacks now run through actual RuntimeRunner acquisition, source paging, ABI dispatch and business transactions on PostgreSQL/PGlite. Sparse middle/trailing ranges, one/all empty chains, exact source checkpoints, complete cross-chain Groups rollback and cold restart are covered. Empty chains retain Down(start1) while separately proved scan completion permits Ready; no synthetic callback or source checkpoint is added.

The closure and its physical range limit are explicit in [source contract](SOURCE-CONTRACT.md). The immutable input is 22b860601c75f2ab3484f3ea32809b1e9a8f38e4ce68ef611b9a45d287248be4, base b92979aed1ed5f396be3365a905e4414d2b1acb0, 24 changed input files; archive SHA256 dbcf11999bb45361926dc1ea343eeded9258116470ccf3dfad45d253d6bb6e96. Post-validation documentation is a separate non-executable increment.

| Accepted selection | Count |
| --- | ---: |
| Groups (maximum 2 concurrent) | 98 |
| Ordinary unique qualified native leaves | 633 |
| Pure / PostgreSQL / PGlite ordinary leaves | 522 / 80 / 31 |
| Race repetitions | 324 |
| Total case executions | 957 |
| Retained v15 ordinary leaves / omitted / new | 559 / 0 / 74 |
| Actual pinned source scenarios / application-exit children | 9 / 27 |
| Test failures / Test skips / timeouts in final queue | 0 / 0 / 0 |

[Commands and exact qualified sets](commands.json) preserve every selected case, original v15 retained set, supplemental new set and actual command/receipt/log/archive hashes. Package messages for no test files are separate from Test skips. Spec and Standards independently reviewed the exact source/tool hashes, then independently audited results. The root audit separately recounted raw package-qualified Go events. Evidence remains at /root/repo/ponder-go/m45-omnichain-logonly-z4-20261103T175258Z-18bognau.

## Failure history and recovery

Original entry/partition/empty-chain activation red failures and their input archives remain retained. Prototype05 misplaced scan validation failed compilation and was corrected. Prototype07 duplicated two shared test helpers; both package failures are preserved (43 store leaves passed within the failed broad command, with no final credit). No earlier red/compile result is counted in the accepted final selection.

Prototype06 put nine PGlite cases into one Go 900s group. Eight leaves passed; the ninth empty_chain_failure was still running at its 74th second when the group alarm fired while opening PGlite for result inspection. The original group failure, EPIPE aftermath, missing leaf and raw stack are preserved. The missing scenario later passed independently on the frozen candidate; every final v16 PGlite scenario is a separate group under the unchanged Go 900s / outer 1050s budgets. The complete final selection was rerun on one input.

Code review found and corrected cleanup registration before an endpoint-rejection return, exact activation-retry lineage binding and a single-empty-page completion mistake. Dedicated pure attacks and real Runner tests cover later-page logs, incomplete/stale/forked scan evidence, proof-free/changed retries, and scope closure. Complete Groups remain one business transaction; raw log positions and the last actual global event are distinct from physical scan heads.

Audit tooling was corrected to bind every PG original/copy log. One review control script was temporarily staged in Mac /tmp; audit computations remained on z4. The identical script was archived under the evidence directory, rerun there, and its local temporary copy removed; its provenance and earlier diagnostics are retained.

PostgreSQL application exits leave the database server running and validate both rollback and activated-state restart. All 27 actual child logs/markers/specs were audited, including new v16, retained v15/v14 and the retained source checkpoint crash boundary. Normal backend shutdown, unchanged private database/role inventory, exited process groups and task-owned data cleanup are independently checked. Successful rebuildable PG engine contents may be cleared after stop proofs; logs, client exit evidence and failures are retained.

## Remaining scope

Formal mapping is still **543/905**, with no credit added by native supplements, race repetitions or these source probes. This is not whole-project completion. Larger sparse physical ranges, mixed closing/log-only chains, ABI-skipped nonempty tails, other omnichain source/lifecycle combinations, PGlite abrupt/database-engine failures, full repository/full-race and complete M45 acceptance remain open. See [next development](NEXT-DEVELOPMENT.md) and the module support matrix.
