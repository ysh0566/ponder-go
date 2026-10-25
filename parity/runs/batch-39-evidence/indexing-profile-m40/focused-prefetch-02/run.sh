#!/bin/bash
set -euo pipefail
source /workspace/scratch/fac5abfa4c9e/ponder-cloud/env.sh
export PATH=/workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-upstream/node_modules/.bin:$PATH
cd /workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-go
export PONDER_INDEXING_PROFILE_NATIVE_OUTPUT="$PWD/parity/runs/indexing-profile-m40/focused-prefetch-02/native-normal.json"
go test -json ./indexing -run '^(TestSourceIndexingProfile|TestSourceIndexingPrefetch|TestClientProfileServiceContext)$' -count=1 > parity/runs/indexing-profile-m40/focused-prefetch-02/normal.jsonl
export PONDER_INDEXING_PROFILE_NATIVE_OUTPUT="$PWD/parity/runs/indexing-profile-m40/focused-prefetch-02/native-race.json"
go test -race -json ./indexing -run '^(TestSourceIndexingProfile|TestSourceIndexingPrefetch|TestClientProfileServiceContext)$' -count=1 > parity/runs/indexing-profile-m40/focused-prefetch-02/race.jsonl
