#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
: "${PONDER_TEST_POSTGRES:?Set the explicit task-only PostgreSQL URL; no environment fallback is used.}"
export GOMODCACHE="$PWD/.toolcache/gomod"
export GOCACHE="$PWD/.toolcache/gobuild"
mkdir -p parity/runs bin
go version > parity/runs/tool-versions.txt
node --version >> parity/runs/tool-versions.txt
../ponder-upstream/packages/core/node_modules/.bin/vitest --version >> parity/runs/tool-versions.txt
anvil --version >> parity/runs/tool-versions.txt
node tools/manifest.mjs
node tools/discovery-audit.mjs
node tools/event-fixtures.mjs
node tools/parameter-fixtures.mjs
node tools/compile-fixtures.mjs
go test -count=1 -json ./abi/testdata/compile/generated > parity/runs/abi-typed-go.jsonl
go test -count=1 -race -json ./abi/testdata/compile/generated > parity/runs/abi-typed-race.jsonl
go test -count=1 -json ./... > parity/runs/go-test.jsonl
go test -count=1 -race -json ./... > parity/runs/go-race.jsonl
go vet ./... > parity/runs/go-vet.txt 2>&1
go build -o bin/ponder ./cmd/ponder
go build -o bin/erc20 ./examples/erc20
CI=1 ../ponder-upstream/packages/core/node_modules/.bin/vitest run --config tools/oracle.config.mjs --configLoader native src/utils/checkpoint.test.ts src/utils/interval.test.ts --reporter=json --outputFile="$PWD/parity/runs/foundation-oracle.json"
CI=1 ../ponder-upstream/packages/core/node_modules/.bin/vitest run --config tools/oracle.config.mjs --configLoader native src/utils/checkpoint.test.ts src/utils/interval.test.ts src/utils/finality.test.ts src/build/schema.test.ts src/utils/decodeEventLog.test.ts src/utils/decodeAbiParameters.test.ts --reporter=json --outputFile="$PWD/parity/runs/core-oracle.json"
node tools/parameters-typecheck.mjs
(cd sdk/typescript/client && CI=1 ./node_modules/.bin/vitest run --configLoader native --reporter=json --outputFile="$PWD/../../../parity/client-original.json")
(cd sdk/typescript/react && CI=1 ./node_modules/.bin/vitest run --typecheck.only --configLoader native --reporter=json --outputFile="$PWD/../../../parity/react-original-typecheck-supported.json")
node tools/differential.mjs
node tools/record-results.mjs
