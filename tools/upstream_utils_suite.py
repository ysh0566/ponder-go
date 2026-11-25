#!/usr/bin/env python3
"""Run every pinned upstream utils file against local archives or its original public URL."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
from provider_archive_nodes import ARCHIVE, archive_nodes


ROOT = Path(__file__).resolve().parents[1]
# Each entry binds the original file and endpoint to the chain holding its data.
ROUTES = [
    ("alchemy", "RPC_URL_ALCHEMY_1", "ethereum"),
    ("chainstack", "RPC_URL_CHAINSTACK_1", "ethereum"),
    ("quicknode", "RPC_URL_QUICKNODE_1", "ethereum"),
    ("ankr", "RPC_URL_ANKR_1", "ethereum"),
    ("infura", "RPC_URL_INFURA_1", "ethereum"),
    ("coinbase", "RPC_URL_COINBASE_8453", "base"),
    ("ankr-tac", "RPC_URL_ANKR_239", "tac"),
    ("altitude", "RPC_URL_ALTITUDE_999", "hyper"),
    ("1rpc", "https://1rpc.io/eth", "1rpc"),
    ("llamarpc", "https://eth.llamarpc.com", "ethereum"),
    ("merkle", "https://eth.merkle.io", "ethereum"),
    ("publicnode", "https://ethereum-rpc.publicnode.com", "ethereum"),
    ("blockpi", "https://ethereum.blockpi.network/v1/rpc/public", "ethereum"),
    ("base", "https://mainnet.base.org", "base"),
    ("avalanche", "https://api.avax.network/ext/bc/C/rpc", "avax"),
    ("moonriver", "https://rpc.api.moonriver.moonbeam.network", "moon"),
    ("celo", "https://forno.celo.org", "celo"),
    ("blast", "https://rpc.blast.io", "blast"),
    ("tron", "https://api.trongrid.io/jsonrpc", "tron"),
    ("zkevm", "https://zkevm-rpc.com", "zkevm"),
    ("arbitrum", "https://arb1.arbitrum.io/rpc", "arbitrum"),
    ("aurora", "https://mainnet.aurora.dev", "aurora"),
    ("cloudflare", "https://cloudflare-eth.com", "cloudflare"),
    ("harmony", "https://api.harmony.one", "harmony"),
    ("optimism", "https://mainnet.optimism.io", "optimism"),
    ("somnia", "https://dream-rpc.somnia.network", "somnia"),
    ("thirdweb", "https://1.rpc.thirdweb.com", "thirdweb"),
    ("zksync", "https://mainnet.era.zksync.io", "zksync"),
]


def run_files(name, files, utils, output, env, typecheck=False):
    if not files:
        raise ValueError(f"empty {name} file selection")
    report = output / (name + ".json")
    command = [str(utils / "node_modules/.bin/vitest"), "run", "--configLoader", "native", "--maxWorkers=1",
               *(["--typecheck.only"] if typecheck else []), "--reporter=json", "--outputFile=" + str(report),
               *sorted(files)]
    with (output / (name + ".log")).open("w") as log:
        status = subprocess.run(command, cwd=utils, env=env, stdout=log, stderr=subprocess.STDOUT).returncode
    (output / (name + ".exit")).write_text(f"{status}\n")
    return 2 if status < 0 or not report.is_file() else status


def run(upstream, archive, output, gateways=None, range_policy_nodes=None, local_only=False):
    ledger = json.loads((ROOT / "parity/test-ledger.json").read_bytes())
    revision = subprocess.check_output(["git", "-C", str(upstream), "rev-parse", "HEAD"], text=True).strip()
    if revision != ledger["baseline"]["revision"]:
        raise ValueError("incorrect upstream revision")
    prefix = "packages/utils/"
    sources = set()
    for entry in ledger["files"]:
        path = entry["path"]
        if path.startswith(prefix):
            if hashlib.sha256((upstream / path).read_bytes()).hexdigest() != entry["sha256"]:
                raise ValueError(f"upstream test source differs: {path}")
            sources.add(path.removeprefix(prefix))
    routes_to_run = [*ROUTES, ("swell", "https://swell-mainnet.alt.technology", "swell")] if gateways else ROUTES
    local = {f"src/_test/{name}.test.ts" for name, _, _ in routes_to_run}
    for name, key, _ in routes_to_run:
        source = (upstream / prefix / f"src/_test/{name}.test.ts").read_text()
        literal = f'"{key}"' if key.startswith("https://") else f"process.env.{key}!"
        if literal not in source:
            raise ValueError(f"endpoint mapping differs from upstream source: {name}")
    types = {file for file in sources if file.endswith(".test-d.ts")}
    public = sources - types - local
    if not local <= sources or any("process.env.RPC_URL_" in (upstream / prefix / file).read_text() for file in public):
        raise ValueError("RPC route inventory does not cover the upstream provider files")

    output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, CI="1")
    env.pop("NODE_OPTIONS", None)
    env.pop("PONDER_LOG_RPC_ROUTES", None)
    utils = upstream / prefix
    with archive_nodes(archive, output, gateways, range_policy_nodes) as servers:
        literals = {url: servers[chain] for _, url, chain in routes_to_run if url.startswith("https://")}
        routes = output / "routes.json"
        routes.write_text(json.dumps(literals, indent=2) + "\n")
        local_env = dict(env, NODE_USE_ENV_PROXY="0", PONDER_LOG_RPC_ROUTES=str(routes),
                         NODE_OPTIONS="--import=" + (ROOT / "tools/historical-log-routes.mjs").as_uri())
        local_env.update({key: servers[chain] for _, key, chain in routes_to_run if key.startswith("RPC_URL_")})
        local_status = run_files("utils-local", local, utils, output, local_env)
    if local_only:
        return local_status
    public_status = run_files("utils-public", public, utils, output, dict(env, NODE_USE_ENV_PROXY="1"))
    types_status = run_files("utils-types", types, utils, output, env, typecheck=True)
    return max(local_status, public_status, types_status)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", required=True, type=Path)
    parser.add_argument("--archive", default=ARCHIVE, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--gateway-binaries", nargs=2, type=Path, metavar=("PROXYD", "ANVIL"),
                        help="run Base and Swell files through actual local proxyd instances")
    parser.add_argument("--range-policy-nodes", nargs=2, metavar=("CORETH", "TAC"), help="loopback JSON-RPC URLs of local development nodes")
    parser.add_argument("--local-only", action="store_true", help="run the complete local partition in an isolated network")
    args = parser.parse_args()
    sys.exit(run(args.upstream.resolve(), args.archive.resolve(), args.output.resolve(), args.gateway_binaries, args.range_policy_nodes, args.local_only))
