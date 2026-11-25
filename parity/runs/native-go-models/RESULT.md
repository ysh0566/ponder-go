# Native Go model and callback verification

The generated model was compiled and used to insert and reread the same row on PostgreSQL 14.24 and PGlite 0.2.13. The test checks binary input ownership, a JSON integer above 2^53, SQL array NULL elements, and point and line coordinates. Two contracts with a shared overloaded function were exercised through generated Go handlers. The calls decode the actual selector and ABI payload and read named tuple array members that have colliding Go identifiers.

| Command | Result |
| --- | --- |
| `go test ./abi ./orm ./config` | All three packages passed |
| `go test -race ./abi ./orm ./config` | All three packages passed |
| `go test -count=1 -run TestOverloadedContractTupleCallbacks ./config` | Four generated handlers compiled and ran |
| `GOFLAGS='-buildvcs=false -race' go test -count=1 -run TestOverloadedContractTupleCallbacks ./config` | Generated caller and code generator passed under the race detector |
| `go test -run 'Test(Normalize\|Column\|DatabaseActions\|.*JSON\|.*Array)' ./store` and the same command with `-race` | Both passed |

Go 1.25.11 ran against the pinned upstream checkout. The worktree used `GOFLAGS=-buildvcs=false` for temporary caller builds. Results here concern Go authoring and do not add to the upstream declaration count.
