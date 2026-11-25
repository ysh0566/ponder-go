#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
: "${PONDER_TEST_POSTGRES:?Set the explicit task-only PostgreSQL URL; no environment fallback is used.}"
export GOMODCACHE="$PWD/.toolcache/gomod"
export GOCACHE="$PWD/.toolcache/gobuild"
# The result collectors below read this directory. A prior focused run may
# have exported a different destination for the standalone package runner.
export PONDER_RUN_DIR="$PWD/parity/runs"
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
export PONDER_CAPTURE_RUN="$capture_run"
node tools/capture-original.mjs --mode=events --output="parity/runs/original-captures/$capture_run/events" --fixture=testdata/runtime/captured-original-events.json
node tools/capture-original.mjs --mode=filters --output="parity/runs/original-captures/$capture_run/filters" --fixture=testdata/runtime/captured-original-filters.json
node tools/capture-original.mjs --mode=blooms --output="parity/runs/original-captures/$capture_run/blooms" --fixture=testdata/bloom/captured-original-blooms.json
node tools/compile-fixtures.mjs
node tools/abi-catalog-source.mjs
node tools/config-declarations-source.mjs > parity/runs/config-declarations-source.json
node tools/value-utilities-source.mjs > parity/runs/value-utilities-source.json
node tools/virtual-events-source.mjs > parity/runs/virtual-events-source.json
node tools/virtual-names-source.mjs > parity/runs/virtual-names-source.json
node tools/handler-contracts-source.mjs > parity/runs/handler-contracts-source.json
node tools/orm-compile-fixtures.mjs
node tools/event-compile-fixtures.mjs
node tools/bloom-compile-fixtures.mjs
node tools/config-chain-catalog.mjs
node tools/config-typed-compile-fixtures.mjs --write
node tools/config-literal-compile-fixtures.mjs --write
node tools/config-pristine-oracle.mjs
node tools/sql-rules.mjs --write
node tools/sql-original.mjs --output="parity/runs/original-captures/$capture_run/sql-pristine"
cp "parity/runs/original-captures/$capture_run/sql-pristine/result.json" parity/runs/sql-original-pristine.json
node tools/sql-original.mjs --capture --output="parity/runs/original-captures/$capture_run/sql" --fixture=testdata/sql/captured-original-sql.json
node tools/sql-vectors.mjs --output=testdata/sql/source-vectors.json
node tools/sql-source-results.mjs
node tools/topic-fixtures.mjs
node tools/topic-source-results.mjs
node tools/source-rpc-fixtures.mjs
node tools/source-range-fixtures.mjs
node tools/source-sync-compile.mjs --write
node tools/source-sync-types.mjs
node tools/source-sync-pristine.mjs
node tools/source-block-fixtures.mjs
node tools/source-child-fixtures.mjs
node tools/source-merge-fixtures.mjs
node tools/source-required-fixtures.mjs
node tools/source-historical-types.mjs
node tools/source-historical-pristine.mjs
node tools/source-historical-types/compile.mjs
node tools/source-trace-fixtures.mjs
node tools/source-realtime-controls.mjs
node tools/source-realtime-observer-probe.mjs
node tools/source-realtime-pristine.mjs
node tools/record-native-inputs.mjs
# Preparations above supply shared inputs. After they succeed, execute every
# independent check and keep its exit code beside this invocation's captures.
verification_status="parity/runs/original-captures/$capture_run/verification.tsv"
printf 'stage\tstatus\n' > "$verification_status"
checks_failed=0
run_check() {
	check_name=$1
	shift
	check_status=0
	"$@" || check_status=$?
	printf '%s\t%s\n' "$check_name" "$check_status" >> "$verification_status"
	if [ "$check_status" -ne 0 ]; then
		printf 'Verification stage %s exited %s\n' "$check_name" "$check_status" >&2
		checks_failed=1
	fi
}
run_check abi-normal go test -count=1 -json ./abi/testdata/compile/generated > parity/runs/abi-typed-go.jsonl
run_check abi-race go test -count=1 -race -json ./abi/testdata/compile/generated > parity/runs/abi-typed-race.jsonl
run_check native-normal node tools/go-tests.mjs
normal_status=$check_status
run_check native-vet go vet ./... > parity/runs/go-vet.txt 2>&1
run_check build-ponder go build -o bin/ponder ./cmd/ponder
run_check build-erc20 go build -o bin/erc20 ./examples/erc20
run_check foundation-original env CI=1 ../ponder-upstream/packages/core/node_modules/.bin/vitest run --config tools/oracle.config.mjs --configLoader native src/utils/checkpoint.test.ts src/utils/interval.test.ts --reporter=json --outputFile="$PWD/parity/runs/foundation-oracle.json"
run_check core-original env CI=1 ../ponder-upstream/packages/core/node_modules/.bin/vitest run --config tools/oracle.config.mjs --configLoader native src/utils/checkpoint.test.ts src/utils/interval.test.ts src/utils/finality.test.ts src/build/schema.test.ts src/utils/decodeEventLog.test.ts src/utils/decodeAbiParameters.test.ts src/build/factory.test.ts src/utils/offset.test.ts src/indexing-store/utils.test.ts src/runtime/fragments.test.ts src/rpc/actions.test.ts --reporter=json --outputFile="$PWD/parity/runs/core-oracle.json"
run_check parameter-types node tools/parameters-typecheck.mjs
run_check client-original sh -c 'cd sdk/typescript/client && CI=1 ./node_modules/.bin/vitest run --configLoader native --reporter=json --outputFile="$PWD/../../../parity/client-original.json"'
run_check react-types sh -c 'cd sdk/typescript/react && CI=1 ./node_modules/.bin/vitest run --typecheck.only --configLoader native --reporter=json --outputFile="$PWD/../../../parity/react-original-typecheck-supported.json"'
run_check differential node tools/differential.mjs
if [ "$normal_status" -eq 0 ]; then
	run_check compiler-cache node tools/compiler-cache-cutover.mjs --phase=normal-to-race
else
	printf 'compiler-cache\tnormal-run-failed\n' >> "$verification_status"
fi
run_check native-race node tools/go-tests.mjs --race
if [ "$checks_failed" -ne 0 ]; then
	printf 'Verification failed. See %s\n' "$verification_status" >&2
	exit 1
fi
run_check config-results node tools/config-live-results.mjs
run_check sync-results node tools/source-sync-results.mjs
run_check historical-results node tools/source-historical-results.mjs
run_check realtime-results node tools/source-realtime-results.mjs
if [ "$checks_failed" -eq 0 ]; then
	run_check parity-results node tools/record-results.mjs
fi
exit "$checks_failed"
