#!/bin/bash
set -euo pipefail
cd /workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-go
source /workspace/scratch/fac5abfa4c9e/ponder-cloud/env.sh
export PATH=/workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-upstream/node_modules/.bin:$PATH
bash /workspace/scratch/fac5abfa4c9e/ponder-cloud/with-postgres.sh bash -c 'export PATH=/workspace/scratch/fac5abfa4c9e/ponder-recovery/work/ponder-upstream/node_modules/.bin:$PATH; node tools/indexing-profile-full-verify.mjs --output=parity/runs/indexing-profile-m40/full-verify-01'
