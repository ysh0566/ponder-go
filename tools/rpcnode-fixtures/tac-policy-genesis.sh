#!/usr/bin/env bash
# Emit a high-start genesis for the TAC v1.6.4 single-validator localnet.
set -euo pipefail
dev_root=${1:?supply the home of an initialized single-validator TAC localnet}
genesis="$dev_root/config/genesis.json"
chain=tacchain_2391337-1
jq -e --arg chain "$chain" '.chain_id == $chain and (.app_state.genutil.gen_txs | length == 1)' "$genesis" >/dev/null
signer=$(tacchaind keys show validator --address --keyring-backend test --home "$dev_root")
account=$(jq -er --arg signer "$signer" '.app_state.auth.accounts[] | select(.address == $signer) | .account_number' "$genesis")

scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
jq '.app_state.genutil.gen_txs[0]
    | .auth_info.fee.gas_limit = "3000000"
    | .auth_info.fee.amount = [{"denom":"utac","amount":"1200000000000000000"}]' "$genesis" > "$scratch/transaction.json"

# Above height one, the genesis validator transaction uses its real account
# number and a metered gas limit.
tacchaind tx sign "$scratch/transaction.json" --home "$dev_root" --from validator \
  --keyring-backend test --chain-id "$chain" --offline --account-number "$account" \
  --sequence 0 --overwrite --output-document "$scratch/signed.json"
jq --slurpfile signed "$scratch/signed.json" \
  '.initial_height = 3115974 | .app_state.genutil.gen_txs = $signed' "$genesis"
