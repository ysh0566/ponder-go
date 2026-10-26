# M44 managed finite readiness evidence

M44 adds managed **finite default-multichain** ownership and durable readiness. Live execution (M45) is not implemented by this milestone. Legacy constructors remain distinct. Persistent PGlite uses actual filesystem/process exclusion, not source stale-heartbeat takeover parity.

Acceptance consists of the successful 73-stage original regression in `../../finite-historical-m43/m44-regression-full-02-20261026/result.json` and the separately recorded `../full-02-corrective-resume-20261026/resume.json`. Normal and race modes each executed 3,463 leaf tests; 107 per mode are M44 supplemental leaves. Original-source credit remains **543/905**, with 362 unimplemented; M44 adds zero original-source credit.

The original M44 wrapper exited 1 because its event parser incorrectly treated the six independently verified no-test packages' package-level Go `skip` records as skipped tests. There were no test-level skips or failures. Its failed result, launcher status, observer status and same-execution receipt remain unchanged. The separately reviewed corrective verifier permits only those exact six empty packages, rejects actual test skips/failures, and completed the previously unrun 14 canonical collectors and summary. Its 20 positive/negative controls and audit are adjacent evidence directories. This is not a retroactive successful wrapper exit.

Shutdown evidence is the original same-execution sampled descendant observation plus actual PostgreSQL shutdown and fixture inventory receipts. It proves the observed task identities/groups drained, not exhaustive fork auditing or host-global process absence. No later execution's PID namespace is used to claim old process absence.

The full-02 prepared archive is distributed as `../../finite-historical-m43/m44-regression-full-02-20261026/prepared-inputs.parts/`. Concatenate manifest-ordered parts and verify the manifest's total size/SHA-256 to reconstruct `prepared-inputs.tar.gz`. Each part is at most 48 MiB. Original local archive and all original reports remain unchanged. Do not extract historical executable inputs into an active checkout.
