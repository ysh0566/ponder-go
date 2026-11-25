# Native Go indexing during RPC outages

A local HTTP proxy returned 503 before startup and again after a successful sync. The application bound its query listener while the RPC was offline. Health and status returned 200, readiness returned 503, and readiness returned 200 after recovery. Anvil 1.8.5 mined a block during the second outage. The PGlite-backed Go application caught up and invoked the block handler exactly once.

Two local WebSocket endpoints exercised initial failover and reconnect after an interrupted stream. The receiver delivered only notifications for the current subscription ID, terminated on a policy rejection, and released its connection on cancellation.

| Go 1.25.11 command | Result |
| --- | --- |
| `go test ./rpc` and `go test -race ./rpc` | Both passed in offline mode |
| `go test -count=1 -run 'Test(Subscription\|Application\|.*FinalizedBlock)' ./runtime ./rpc` and the same command with `-race` | Both packages passed in each mode |

The separate 17 live gateway assertions retain their previously recorded results. [Provider results](../provider-policy-assertions/RESULT.md)
