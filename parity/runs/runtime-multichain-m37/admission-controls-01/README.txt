Commands (env.sh, pinned Go; no database tests):
go test -json ./runtime ./rpc -run 'TestSourceHistorical(InitialAdmission|MemoryBound|FactoryDrain|ProviderOwnership|MergeVectors)$|TestSourceBlock' -count=1
go test -race -json ./runtime ./rpc -run 'TestSourceHistorical(InitialAdmission|MemoryBound|FactoryDrain|ProviderOwnership|MergeVectors)$|TestSourceBlock' -count=1
Both exit 0. Supplementary controls, zero original source credit. MemoryBound assertion body unchanged; requester now admits synchronously through BeginCall and completes separately. InitialAdmission includes exact 100 ordered block/trace initial requests, serialized synchronous compatibility, first-error identity and cancel/join completion. Actual source/native stop probe validation remains owned by unified replay runner.
