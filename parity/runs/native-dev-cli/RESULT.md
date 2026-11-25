# Native Go project development

A temporary Go project was started with `dev --schema ponder_go_reloads --hostname 127.0.0.1 --port 0`. A syntax error left the original child process alive. The repaired Go source rebuilt successfully and the previous child handled SIGINT before the replacement started. Updating only schema.json started a third generation. Each generation received a different shadow schema, and the listener options reached the child. Both start and serve propagated the same options and refused an invalid TCP port.

Go 1.25.11 passed `go test -count=1 ./cmd/ponder` and the same command with `-race`. The real HTTP listener test passed normally and under the race detector with `go test -count=1 -run TestApplicationServeOnly ./runtime` using PGlite. The application honored the CLI environment over unusable programmatic defaults and shut down normally.
