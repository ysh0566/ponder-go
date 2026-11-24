# Historical logs and gateway limits on Base, Swell and Tron

The complete inventory contains 905 upstream declarations at Ponder revision `17c6d5ba0648c1a29bcb74c7d4c56733a4ec5334`. Of those, **874 passed, 19 failed, 10 were skipped upstream and two were marked todo upstream**. The three new utils reports cover all 101 utils declarations. The six reports for the other packages come from the preceding full-suite run at the same upstream revision. Each report's SHA-256 and each unresolved declaration's source ID appear in the [complete inventory](upstream.json).

| Chain | Original historical query | Original range assertion |
| --- | --- | --- |
| Base | The archive and proxyd each returned 77 logs. | The archive returned all 2,420 logs directly. With the same backend capped at 256 KiB/s, proxyd hit its two-second timeout and returned HTTP 503. |
| Swell | Anvil returned zero logs. The original assertion requires 58 historical mainnet logs. | Proxyd enforced the configured 1,000-block limit and the original retry assertions passed. |
| Tron | TronGrid supplied four logs. Independent collections produced identical page and manifest bytes. | A second capture confirmed RPC error `-32602`. The rejected query ends one block after the historical index. Its end block's parent hash matches the index anchor. |

The full utils run produced 77 passes, 19 failures, four original skips and one original todo. Eighteen failures concern provider range, size or transport errors. The remaining failure is the Swell historical query. Both core database reports have 723 passes, six skips and one todo each.

The Go RPC package passed in a clean checkout with external networking disabled. Normal and race builds each passed 23 top-level tests and 173 subtests, including all 24 mapped provider cases. This phase adds the two Tron mappings, bringing the formal ledger to **567 mapped and 338 unimplemented**. The last certified module-wide native pass count is 543. The repository's 500 Node controls and 35 Python tests passed.

[Case results, node versions, data hashes and native test hashes](results.json) · [Reproduction commands](../../../docs/HISTORICAL_LOG_RPC.md#run-base-and-swell-behind-a-real-gateway)
