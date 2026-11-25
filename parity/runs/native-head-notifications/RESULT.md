# WebSocket notifications in native indexers

Go 1.25.11, Anvil 1.8.5, PostgreSQL 14.24 and PGlite 0.2.13 exercised the public Go runtime. The direct chain configuration and the compiled source configuration both carry the optional `WS` endpoint.

The test gateway relays actual Anvil header notifications. HTTP RPC for indexed data remains available independently. With a 60-second poll interval, the worker reached readiness while the subscription handshake returned HTTP 503. A block mined during this outage was acquired when the first subscription succeeded. The next block was indexed on notification. The test then cut the WebSocket, observed a rejected reconnection and mined a third block. The fourth block's notification after reconnect caused the worker to acquire both missing blocks. Each assertion had a 12-second deadline.

Changing only the subscription endpoint preserved the identity of processed ranges. With regular polling set to 40 milliseconds and the subscription unavailable, the worker still indexed block 5. The test also rejected an HTTP URL in the WebSocket field.

- `go test -count=1 -run '^(TestHeadNotifications|TestIsolatedIndexer|TestIsolatedBoundedCompletion|TestContinuousFinality|TestApplicationRPCRecovery|TestSourceConfigurationRuntime)$' ./runtime`, also with `-race`
- `go test -count=1 -run '^(TestSubscriptionReconnect|TestSubscriptionPermanentPolicyClose)$' ./rpc`, also with `-race`
- `go test -run '^$' ./...` and `go vet ./...`

The native supplements leave upstream case counts unchanged.
