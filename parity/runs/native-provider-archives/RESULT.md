# Native provider tests on self-hosted archives

The default Go RPC package now exercises 22 mapped declarations from 19 original upstream files. Twenty-one cases send the original historical log filter and check its result. The Moonriver range case checks the internal RPC error code, the message and the exact retry plan. The two declarations named `ankr success` have distinct source IDs, networks and Go subtests.

The package passed on a clean checkout in a container with external networking disabled, using Go 1.25.11 and Python 3.11.2. Both standard and race builds passed 23 top-level tests and 171 subtests, including all 22 provider cases. The reviewed archive adds 8,628,530 compressed bytes and supplies seven indexes. Together with the earlier three-chain fixtures it supports the same nine loopback listeners for Go and Vitest. The native input fingerprint covers the server, launcher and fixtures.

A fresh pinned upstream checkout ran all 101 utils declarations. The 22 declarations matched by source ID and assertion digest passed. The complete profile produced 75 passed, 21 failed, four skipped and one todo. Existing service-specific failures and the missing historical Swell query retain their upstream outcomes.

Rebuilding the discovery manifest yielded **565 mapped and 340 unimplemented** across 905 declarations. The last accepted module-wide pass count is 543. This phase records package-level execution evidence for the 22 added mappings. The earlier M43 test-file inventory stays fixed. Its registration check also recognizes later native tests that have source mappings and still rejects an unregistered supplemental file.

[Source IDs, target subtests and report hashes](results.json) · [Offline reproduction](../../../docs/HISTORICAL_LOG_RPC.md#run-the-native-provider-cases-offline)
