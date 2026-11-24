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
CHAINS = ("ethereum", "base", "tac", "hyper", "avax", "moon", "celo", "blast", "zkevm")


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


@contextmanager
def archive_nodes(archive, output):
    with ExitStack() as stack:
        indexes = Path(stack.enter_context(tempfile.TemporaryDirectory(dir=output)))
        extract_bundle(archive, "04bb929c0e160d13784115650afe8131df3f937ee90ea12dd404c641ad61c858", indexes)
        extract_bundle(FIXTURES / "historical-blast-zkevm.tar.gz",
                       "b49f105099568c1b88527d5e80bdeae3fe11c3adbf1a360f5f269c2f208f0d4c", indexes)
        celo = FIXTURES / "celo-10000000-10001000.json.gz"
        if hashlib.sha256(celo.read_bytes()).hexdigest() != "13017272c4960fb1da5e08b7bdb2fa0d5d528a59d87af3245dd275200d6df06c":
            raise ValueError("Celo capture differs from the reviewed fixture")
        yield {name: start_server(name, indexes, output, stack) for name in CHAINS}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path, help="existing temporary directory for indexes and server logs")
    args = parser.parse_args()
    with archive_nodes(ARCHIVE, args.directory) as urls:
        print(json.dumps(urls), flush=True)
        sys.stdin.buffer.read(1)
