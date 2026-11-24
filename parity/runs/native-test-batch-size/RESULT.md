# Native test batch sizing

The runner accepts a positive `PONDER_GO_TEST_BATCH_SIZE` and records the effective size in its execution report. The default is four functions. A size of one gives each top-level function its own Go package timeout window, which helps with historical and storage suites whose grouped cases can exceed ten minutes.

## Validation

Go 1.25.11 executed the same 35 distinct checkpoint and interval cases in all three runs. The default schedule had two batches. Both single-function schedules had three batches. Every discovered function ran once, and each command used the default Go timeout. The input fingerprint matched before and after each run. Size zero failed before creating an output directory.

```sh
PONDER_RUN_DIR=parity/runs/native-test-batch-size/default \
  node tools/go-tests.mjs --package=./checkpoint --package=./interval
PONDER_GO_TEST_BATCH_SIZE=1 PONDER_RUN_DIR=parity/runs/native-test-batch-size/single \
  node tools/go-tests.mjs --package=./checkpoint --package=./interval
PONDER_GO_TEST_BATCH_SIZE=1 PONDER_RUN_DIR=parity/runs/native-test-batch-size/single \
  node tools/go-tests.mjs --package=./checkpoint --package=./interval --race
PONDER_GO_TEST_BATCH_SIZE=0 PONDER_RUN_DIR=parity/runs/native-test-batch-size/invalid \
  node tools/go-tests.mjs --package=./checkpoint
```
