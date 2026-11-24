# Native test execution and failure reporting

A Go execution now records the terminal result for each batch and counts passed, failed and skipped top-level functions. The report names listed functions that never started and test paths that started but never emitted a terminal event. A package discovery failure is retained alongside subsequent packages' results. A failed assertion can have a complete execution. A build error, an unstarted test or an unfinished test prevents a complete result.

The default batch size is four and the default package deadline still comes from Go. Setting `PONDER_GO_TEST_TIMEOUT_MINUTES` selects a positive whole-minute deadline for a Go test invocation. The report retains the effective deadline and exact command. Upstream test bodies and Vitest deadlines are unchanged.

## Historical integration matrix

The complete `TestRuntimeOmnichainLogOnlySource` matrix passed all 18 scenarios in both modes on PostgreSQL and PGlite. The normal run took 653.61 seconds and the race run took 1163.00 seconds with a 60-minute Go package deadline. Both runs exercised the same nine scenarios per backend in one invocation of the original top-level test.

## Verified behavior

- Real Go failures were exercised on a disposable four-package module in both normal and race modes. One package failed to compile. A second produced two passes, an assertion failure and a skip. In a third package the test process exited inside a child test. Its parent and child were reported unfinished and the following listed function was reported unstarted. The final package had no test files.
- Checkpoint and interval passed the same 35 leaf cases using the default schedule and using single-function batches with a recorded 60-minute deadline in both modes.
- Eleven project packages passed the same 1,564 leaf cases in normal and race modes. These include the 35 checkpoint and interval cases. The input fingerprint covers 1,337 files. Package counts, command-log hashes and execution-report hashes are in [cases.json](cases.json).
- The shell orchestration controls covered failure of the normal Go run, an SDK suite and a result collector. Independent stages continued, the shell returned nonzero, and the aggregate result was withheld. The control for a fully successful sequence reached the aggregate result. Each run wrote its own stage exit codes in `verification.tsv`.
- The repository Node controls passed 520 cases. The Python suite passed 23 tests.

The formal port ledger remains at 543 of 905. The diagnostic module and verification-driver controls are separate from the project and pinned original test counts.
