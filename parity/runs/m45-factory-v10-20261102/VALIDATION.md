# Factory v10 verified milestone

581 unique native cases passed on one frozen 120-file input: 249 pure, 190 PostgreSQL, 142 PGlite. All 134 groups exited zero; no Test failures or skips. The previous 530 cases were rerun on this snapshot, with 51 new supplemental cases. Original-source mapping remains 543/905; no mapping credit was added.

Supported scope: opt-in managed log-address factory execution in default multichain ordering, with Live, SourceCheckpoints and FactorySources enabled, under both RPC cache policies. Child addresses become effective at their birth block, including earlier child logs in that block. Factory discovery bounds do not expire an already discovered child. Authenticated branch reconstruction preserves provenance across historical/live handoff, retry, reopen and reorg.

Transaction/trace/transfer factory runtime paths, other ordering modes and CLI integration remain unsupported or pending. Complete pinned upstream realtime/SQL factory oracle, factory-specific application-process crash tests, full/race suites and database-engine/server/power-loss crash acceptance remain separate. Prefix reconstruction is linear historical RPC work and requires archival-provider availability, including warm-cache restarts. Startup RPC-cache fencing remains an explicit compatibility difference. This is not complete M45 acceptance.

Source fingerprint: 2229fa9d3d8dc29b72816458c8ad8e4a71629a0f8b44db4db4c7c2639a0c6edf

Original evidence (read-only reference): /root/repo/ponder-go/m45-factory-z4-20261102T140000Z

This independent release evidence directory preserves exact paths and SHA256 references to original logs, receipts, input archives and historical failures. Original evidence and old seals were not modified.

The final independent result review and SHA256SUMS bind this report, audit and POSTCHECK. Publication uses a separate worktree derived from c2eb45aa885c906df416e954e1b0671b60d6ab57.

## Verification evidence

Independent final result review: 0 findings. Original 134 group logs, receipts, commands, input archive members and modes, patches and expected leaf sets were rechecked. Existing 2,283 checksum entries across the three previous seals match.

Release evidence: /root/repo/ponder-go/m45-factory-release-z4-20261102T191144Z-y_25xt8g

Audit seal SHA256: 3607ea5cfcd95594cc9294a637d4ec2f9df5817d9f4cddcc5e3b1b196aa5a19f

Validation audit SHA256: 8f37446ff4a027be560db8cb65f1dbf6dd242533397158c95015ed5cdb6666ce

Independent result review SHA256: 903f01243b22cbc16e5561747ccc489d139fe56dbd5f2a21e1f7d473862decad

The exact command template, per-group package and selection, pass counts and original evidence hashes are recorded in commands.json. Full expected leaf sets are bound by the independent audit and its seal. The 581-case run is a selected regression set, not a full-repository or race run. No tests were rerun for this publication-only restoration; all tested source inputs were checked byte-for-byte and mode-for-mode.
