# Publish PostgreSQL views at first catch-up

The generated Go project passes its indexer through `runtime.PublishViewsOnReady` when started with `--views-schema` or `DATABASE_VIEWS_SCHEMA`. It reports readiness after the stable views are committed. A bounded indexer returning successfully also publishes. Each running instance publishes once. If publication fails, the wrapper cancels indexing and returns the database error.

- On PostgreSQL 14.24 and Foundry Anvil 1.8.5, a real Go block callback paused before its first commit. The old published row remained visible and the new process returned HTTP 503 on `/ready`. After the handler committed, the process reported 200, and an SSE connection open before the switch received the new deployment's existing row without another write.
- The test switched the stable name manually after a worker first reported ready, then made that worker fall behind and catch up. The worker respected the manual switch. A finite worker with no explicit readiness callback also published on successful completion.
- While a separate transaction held the publication lock, the ready flag stayed false. Cancelling the blocked indexer left the old views intact.
- A dependent SQL view prevented an incompatible change of column type. The application returned a publication error, kept readiness false and preserved the previous published row.
- `TestGeneratedGoStartViews` generated an independent Go module using `ponder init` and launched the real `start --views-schema` command against a self-hosted Anvil chain. It read chain 1's checkpoint through the newly published schema.

[Application and stable HTTP/SSE stream test](../../../runtime/published_views_test.go) · [CLI and generated module](../../../cmd/ponder/serve_queries_test.go)

## Verification

With the local Anvil binary and a disposable PostgreSQL database configured, the readiness test passed three consecutive runs in each mode. The generated project's CLI tests passed ordinary and race execution. The full module compiled and passed vet.

```sh
go test -count=3 ./runtime -run '^TestPublishViewsOnReady$'
go test -race -count=3 ./runtime -run '^TestPublishViewsOnReady$'
go test -count=1 ./cmd/ponder
go test -race -count=1 ./cmd/ponder
go test -run '^$' ./...
go vet ./...
```
