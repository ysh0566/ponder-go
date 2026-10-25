Commands (env.sh, pinned Go, no database tests):
go test -json ./runtime ./rpc -run 'TestSourceHistorical(InitialAdmission|AcquisitionFirstError|MemoryBound|FactoryDrain|ProviderOwnership|MergeVectors)$|TestSourceBlock' -count=1
go test -race -json ./runtime ./rpc -run 'TestSourceHistorical(InitialAdmission|AcquisitionFirstError|MemoryBound|FactoryDrain|ProviderOwnership|MergeVectors)$|TestSourceBlock' -count=1
Both exit 0; each 81 passing test nodes, no failure or skip.
Adds 8 first-error leaves: asynchronous admitted versus synchronous requesters, initiating block versus trace failure, sibling returning cancellation versus its own later distinct error. First failure must complete without external cancel, retain identity, and join both request owners. Earlier admission-controls-01 evidence retained unchanged.
Supplemental controls add zero original source credit. Real source/native dual-backend probes remain owned by the unified replay runner.
