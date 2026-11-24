#!/usr/bin/env python3
"""Run every pinned upstream utils file against local archives or its original public URL."""
import argparse
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile


ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tools/rpcnode-fixtures"
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
    ("1rpc", "https://1rpc.io/eth", "ethereum"),
    ("llamarpc", "https://eth.llamarpc.com", "ethereum"),
    ("merkle", "https://eth.merkle.io", "ethereum"),
    ("publicnode", "https://ethereum-rpc.publicnode.com", "ethereum"),
    ("blockpi", "https://ethereum.blockpi.network/v1/rpc/public", "ethereum"),
    ("base", "https://mainnet.base.org", "base"),
    ("avalanche", "https://api.avax.network/ext/bc/C/rpc", "avax"),
    ("moonriver", "https://rpc.api.moonriver.moonbeam.network", "moon"),
    ("celo", "https://forno.celo.org", "celo"),
    ("blast", "https://rpc.blast.io", "blast"),
    ("zkevm", "https://zkevm-rpc.com", "zkevm"),
]


def extract_bundle(path, expected, destination):
    with path.open("rb") as stream:
        if hashlib.file_digest(stream, "sha256").hexdigest() != expected:
            raise ValueError(f"archive digest does not match the reviewed fixture: {path}")
        stream.seek(0)
        with tarfile.open(fileobj=stream, mode="r:gz") as bundle:
            bundle.extractall(destination, filter="data")


def stop_server(process):
    try:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
    finally:
        process.stdout.close()


def start_server(name, indexes, output, stack):
    args = [str(indexes / ("index-" + name))]
    if name == "ethereum":
        args.append(str(indexes / "index-weth"))
    if name == "moon":
        args += ["--error-capture", str(FIXTURES / "moonriver-error.json")]
    if name == "celo":
        args = ["--log-capture", str(FIXTURES / "celo-10000000-10001000.json.gz")]
    log = stack.enter_context((output / (name + ".log")).open("w"))
    process = subprocess.Popen([sys.executable, "-B", str(ROOT / "tools/historical_log_rpc.py"), "serve", *args],
                               stderr=log, stdout=subprocess.PIPE, text=True)
    stack.callback(stop_server, process)
    url = process.stdout.readline().strip()
    if not url.startswith("http://127.0.0.1:"):
        raise RuntimeError(f"could not start {name} archive, see {log.name}")
    return url


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


def run(upstream, archive, output):
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
    local = {f"src/_test/{name}.test.ts" for name, _, _ in ROUTES}
    for name, key, _ in ROUTES:
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
    evidence = json.loads((ROOT / "parity/runs/self-hosted-log-replay/cases.json").read_bytes())
    extra = json.loads((ROOT / "parity/runs/historical-log-availability/results.json").read_bytes())
    with ExitStack() as stack:
        indexes = Path(stack.enter_context(tempfile.TemporaryDirectory(dir=output)))
        extract_bundle(archive, evidence["portableArchive"]["sha256"], indexes)
        supplemental = FIXTURES / "historical-blast-zkevm.tar.gz"
        extract_bundle(supplemental, extra["files"][supplemental.name]["sha256"], indexes)
        celo = FIXTURES / "celo-10000000-10001000.json.gz"
        if hashlib.sha256(celo.read_bytes()).hexdigest() != extra["files"][celo.name]["sha256"]:
            raise ValueError("Celo capture differs from the reviewed fixture")
        servers = {name: start_server(name, indexes, output, stack) for name in sorted({r[2] for r in ROUTES})}
        literals = {url: servers[chain] for _, url, chain in ROUTES if url.startswith("https://")}
        routes = output / "routes.json"
        routes.write_text(json.dumps(literals, indent=2) + "\n")
        local_env = dict(env, PONDER_LOG_RPC_ROUTES=str(routes),
                         NODE_OPTIONS="--import=" + (ROOT / "tools/historical-log-routes.mjs").as_uri())
        local_env.update({key: servers[chain] for _, key, chain in ROUTES if key.startswith("RPC_URL_")})
        local_status = run_files("utils-local", local, utils, output, local_env)
    public_status = run_files("utils-public", public, utils, output, dict(env, NODE_USE_ENV_PROXY="1"))
    types_status = run_files("utils-types", types, utils, output, env, typecheck=True)
    return max(local_status, public_status, types_status)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--upstream", required=True, type=Path)
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    sys.exit(run(args.upstream.resolve(), args.archive.resolve(), args.output.resolve()))
