#!/bin/bash
set -euo pipefail
source /workspace/scratch/fac5abfa4c9e/ponder-cloud/env.sh
export PATH=/workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-upstream/node_modules/.bin:$PATH
cd /workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-go
go test -json ./store -run '^TestSourceRPCAsyncRead' -count=1 > parity/runs/indexing-profile-m40/async-reader-focused-01/normal.jsonl
go test -race -json ./store -run '^TestSourceRPCAsyncRead' -count=1 > parity/runs/indexing-profile-m40/async-reader-focused-01/race.jsonl
