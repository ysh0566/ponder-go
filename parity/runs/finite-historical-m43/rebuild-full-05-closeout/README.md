# M43 local acceptance and publication boundary

The 73 full05 stages, 14 canonical collectors and summary completed successfully. Normal and race each passed 3,356 leaf cases (98 batches), with zero failed/skipped cases. This adds **zero original-source credit**: 543/905 mapped; 362 remain.

The outer launcher nevertheless exited unsuccessfully: its output allowlist omitted `parity/test-ledger.json`, an existing output of the unchanged summary writer. That failure is not rewritten. Independent review and a replay starting from the frozen pre-ledger produced byte-identical ledger and summary outputs. All 3,152 changed ledger leaves were existing evidence pointers; mappings, assertions, statuses and coverage stayed unchanged. The replay retained the original validation logic and redirected only the ledger input and two derived output writes. `result.json` records this separate corrective closeout, not a retroactive successful launcher.

`replay-audit.json` is an explicitly path-normalized public export. The exact reviewed replay helper retains the original execution-root binding and is not a general-purpose sandbox. Private whole-tree manifests and reconstruction drafts are not published. No credential authentication, rotation or history rewrite was performed.

The privacy-reviewed prepared archive is represented losslessly by four chunks (each at most 48 MiB). Verify/reassemble without extracting any members:

```
python3 tools/reassemble-m43-evidence.py parity/runs/finite-historical-m43/rebuild-full-05/prepared-inputs.parts/manifest.json --verify-only
# Omit --verify-only to create the archive only when it does not already exist.
```

`publication-representation.json` maps required nonduplicated loose artifact references to their exact archived members and hashes. Historical executable/source members are never extracted into the active checkout by this helper. The package retains historical absolute-path provenance and is not a self-contained portable execution environment.

The original **pre-source** snapshot remains private because it contains historical local-test credential URIs and private reconstruction information. Its sealed hash is disclosed, but this public package does **not** claim to reconstruct it. Necessary safe pre-ledger bytes and corrective evidence are included separately. Current public documentation has been redacted; historical credential validity/reuse is unknown. Existing published history was not rewritten.
