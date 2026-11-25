#!/usr/bin/env python3
"""Serve the reviewed historical RPC fixtures until the controlling stdin closes."""
import argparse
from contextlib import contextmanager, ExitStack
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "tools/rpcnode-fixtures"
ARCHIVE = ROOT / "testdata/rpc/provider-indexes.tar.gz"
PUBLIC_ERRORS = ("1rpc", "arbitrum", "aurora", "cloudflare", "harmony", "somnia", "thirdweb", "zksync")
CHAINS = ("ethereum", "base", "tac", "hyper", "avax", "moon", "celo", "blast", "zkevm", "tron", "optimism", *PUBLIC_ERRORS)


def extract_bundle(path, expected, destination):
    with path.open("rb") as stream:
        if hashlib.file_digest(stream, "sha256").hexdigest() != expected:
            raise ValueError(f"archive digest does not match the reviewed fixture: {path}")
        stream.seek(0)
        with tarfile.open(fileobj=stream, mode="r:gz") as bundle:
            for member in bundle:
                name = Path(member.name)
                if name.is_absolute() or ".." in name.parts or not member.isfile():
                    raise ValueError(f"unexpected fixture archive member: {member.name}")
                target = destination / name
                target.parent.mkdir(parents=True, exist_ok=True)
                with bundle.extractfile(member) as source, target.open("xb") as out:
                    shutil.copyfileobj(source, out)


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


def start_server(name, indexes, output, stack, bytes_per_second=None, range_policy_url=None):
    # Provider-specific errors stay on that provider's listener.
    directories = {"ethereum": ("ethereum", "weth"), "optimism": ("optimism", "optimism-weth"),
                   "1rpc": ("ethereum",), "cloudflare": ("ethereum",)}.get(name, (name,))
    args = [str(indexes / ("index-" + directory)) for directory in directories]
    if name in PUBLIC_ERRORS:
        args += ["--error-capture", str(indexes / "public-errors" / (name + ".json"))]
    if name == "1rpc":
        args += ["--error-capture", str(indexes / "public-errors/1rpc-narrow.json")]
    if name in ("moon", "tron"):
        capture = "moonriver-error.json" if name == "moon" else "tron-error.json"
        args += ["--error-capture", str(FIXTURES / capture)]
    if range_policy_url is not None:
        args += ["--range-policy-url", range_policy_url]
    if bytes_per_second is not None:
        args += ["--bytes-per-second", str(bytes_per_second)]
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


@contextmanager
def archive_nodes(archive, output, gateways=None, range_policy_nodes=None):
    with ExitStack() as stack:
        indexes = Path(stack.enter_context(tempfile.TemporaryDirectory(dir=output)))
        extract_bundle(archive, "04bb929c0e160d13784115650afe8131df3f937ee90ea12dd404c641ad61c858", indexes)
        extract_bundle(FIXTURES / "historical-blast-zkevm.tar.gz",
                       "b49f105099568c1b88527d5e80bdeae3fe11c3adbf1a360f5f269c2f208f0d4c", indexes)
        extract_bundle(FIXTURES / "tron-and-base-indexes.tar.gz",
                       "e6763e4f812e41c91d031a7c5c7a4c952345e1ca93a116ef239fea8741e4b70a", indexes)
        extract_bundle(FIXTURES / "public-provider-indexes.tar.gz",
                       "e7f7378fa90ad3fc3537c5f07335f7a44788a87bb5e55d9ea6cea517c47f6cb8", indexes)
        celo = FIXTURES / "celo-10000000-10001000.json.gz"
        if hashlib.sha256(celo.read_bytes()).hexdigest() != "13017272c4960fb1da5e08b7bdb2fa0d5d528a59d87af3245dd275200d6df06c":
            raise ValueError("Celo capture differs from the reviewed fixture")
        policies = dict(zip(("avax", "tac"), range_policy_nodes, strict=True)) if range_policy_nodes else {}
        nodes = {name: start_server(name, indexes, output, stack, range_policy_url=policies.get(name)) for name in CHAINS}
        if gateways:
            from provider_gateway_nodes import start_gateways
            backend = start_server("base-full", indexes, output, stack, bytes_per_second=256*1024)
            nodes.update(start_gateways(*gateways, backend, output, stack))
        yield nodes


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path, help="existing temporary directory for indexes and server logs")
    args = parser.parse_args()
    with archive_nodes(ARCHIVE, args.directory) as urls:
        print(json.dumps(urls), flush=True)
        sys.stdin.buffer.read(1)
