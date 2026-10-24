#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
: "${PONDER_TEST_POSTGRES:?Set the explicit task-only PostgreSQL URL; no environment fallback is used.}"
export GOMODCACHE="$PWD/.toolcache/gomod"
export GOCACHE="$PWD/.toolcache/gobuild"
export PATH="$PWD/../ponder-upstream/node_modules/.bin:$PATH"
mkdir -p parity/runs bin
go version > parity/runs/tool-versions.txt
node --version >> parity/runs/tool-versions.txt
../ponder-upstream/packages/core/node_modules/.bin/vitest --version >> parity/runs/tool-versions.txt
anvil --version >> parity/runs/tool-versions.txt
node tools/manifest.mjs
node tools/discovery-audit.mjs
node tools/event-fixtures.mjs
node tools/parameter-fixtures.mjs
node tools/factory-fixtures.mjs
node tools/protocol-fixtures.mjs
node tools/runtime-fixtures.mjs
node tools/selected-runtime-oracle.mjs
node tools/selected-runtime-oracle.mjs --mapped-only
node tools/event-build-fixtures.mjs
node tools/rpc-actions-fixtures.mjs
node tools/bloom-fixtures.mjs
capture_run="$(date -u +%Y%m%dT%H%M%S)-$$"
node tools/capture-original.mjs --mode=events --output="parity/runs/original-captures/$capture_run/events" --fixture=testdata/runtime/captured-original-events.json
node tools/capture-original.mjs --mode=filters --output="parity/runs/original-captures/$capture_run/filters" --fixture=testdata/runtime/captured-original-filters.json
node tools/capture-original.mjs --mode=blooms --output="parity/runs/original-captures/$capture_run/blooms" --fixture=testdata/bloom/captured-original-blooms.json
node tools/compile-fixtures.mjs
node tools/orm-compile-fixtures.mjs
node tools/event-compile-fixtures.mjs
node tools/bloom-compile-fixtures.mjs
node tools/config-chain-catalog.mjs
node tools/config-pristine-oracle.mjs
node tools/record-native-inputs.mjs
go test -count=1 -json ./abi/testdata/compile/generated > parity/runs/abi-typed-go.jsonl
go test -count=1 -race -json ./abi/testdata/compile/generated > parity/runs/abi-typed-race.jsonl
node tools/go-tests.mjs
node tools/go-tests.mjs --race
go vet ./... > parity/runs/go-vet.txt 2>&1
go build -o bin/ponder ./cmd/ponder
go build -o bin/erc20 ./examples/erc20
CI=1 ../ponder-upstream/packages/core/node_modules/.bin/vitest run --config tools/oracle.config.mjs --configLoader native src/utils/checkpoint.test.ts src/utils/interval.test.ts --reporter=json --outputFile="$PWD/parity/runs/foundation-oracle.json"
CI=1 ../ponder-upstream/packages/core/node_modules/.bin/vitest run --config tools/oracle.config.mjs --configLoader native src/utils/checkpoint.test.ts src/utils/interval.test.ts src/utils/finality.test.ts src/build/schema.test.ts src/utils/decodeEventLog.test.ts src/utils/decodeAbiParameters.test.ts src/build/factory.test.ts src/utils/offset.test.ts src/indexing-store/utils.test.ts src/runtime/fragments.test.ts src/rpc/actions.test.ts --reporter=json --outputFile="$PWD/parity/runs/core-oracle.json"
node tools/parameters-typecheck.mjs
(cd sdk/typescript/client && CI=1 ./node_modules/.bin/vitest run --configLoader native --reporter=json --outputFile="$PWD/../../../parity/client-original.json")
(cd sdk/typescript/react && CI=1 ./node_modules/.bin/vitest run --typecheck.only --configLoader native --reporter=json --outputFile="$PWD/../../../parity/react-original-typecheck-supported.json")
node tools/differential.mjs
node tools/config-live-results.mjs
node tools/record-results.mjs
