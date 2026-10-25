#!/bin/bash
set -eu
out="$PWD/parity/runs/runtime-historical-m36/local-controls-mutations-01"
for spec in 'sequential range_RPC_prefetch_and_ordered_commits' 'swallow_error prefetch_error_preserves_prior_interval' 'unbound interval_failure_rolls_back_rows'; do
 read -r mutant test <<< "$spec"
 cmd=(go test -json -count=1 -timeout=90s -overlay="$out/$mutant/overlay.json" ./runtime -run "^TestSourceLocalHistoricalControls$/(postgres|pglite)/^${test}$")
 printf '%q ' "${cmd[@]}" > "$out/$mutant/command.txt"; printf '\n' >> "$out/$mutant/command.txt"
 set +e; "${cmd[@]}" > "$out/$mutant/red.jsonl" 2> "$out/$mutant/red.stderr"; code=$?; set -e
 echo "$code" > "$out/$mutant/red.exit"
done
cmd=(go test -json -count=1 -timeout=90s ./runtime -run '^TestSourceLocalHistoricalControls$/(postgres|pglite)/^(range_RPC_prefetch_and_ordered_commits|prefetch_error_preserves_prior_interval|interval_failure_rolls_back_rows)$')
printf '%q ' "${cmd[@]}" > "$out/green-command.txt"; printf '\n' >> "$out/green-command.txt"
set +e; "${cmd[@]}" > "$out/green.jsonl" 2> "$out/green.stderr"; code=$?; set -e
echo "$code" > "$out/green.exit"
exit "$code"
