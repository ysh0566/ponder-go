"""Capture canonical block headers and contract logs for a simulation replay."""
import argparse
import gzip
import json
import os
import pathlib
import time
import urllib.error
import urllib.request

BLOCK_FIELDS = ("number", "hash", "parentHash", "timestamp", "logsBloom")
LOG_FIELDS = ("blockNumber", "blockHash", "transactionIndex", "transactionHash",
              "logIndex", "address", "topics", "data", "removed")


def request(endpoint, messages):
    data = json.dumps(messages).encode()
    req = urllib.request.Request(endpoint, data=data, headers={"Content-Type": "application/json"})
    for attempt in range(5):
        try:
            with urllib.request.urlopen(req, timeout=45) as response:
                return json.load(response)
        except urllib.error.HTTPError as exc:
            if attempt == 4 or exc.code not in (429, 502, 503, 504):
                raise
            retry_after = exc.headers.get("Retry-After", "")
            delay = int(retry_after) if retry_after.isdecimal() else 2 ** (attempt + 1)
            time.sleep(delay)


def message(method, params, ident):
    return {"jsonrpc": "2.0", "id": ident, "method": method, "params": params}


def capture(endpoint, start, end, address, selectors):
    logs = []
    headers = []
    for lo in range(start, end + 1, 8):
        heights = range(lo, min(lo + 8, end + 1))
        batch = [message("eth_getBlockByNumber", [hex(n), False], n) for n in heights]
        replies = {entry["id"]: entry["result"] for entry in request(endpoint, batch)}
        headers.extend(replies[n] for n in heights)
        filt = {"fromBlock": hex(lo), "toBlock": hex(heights[-1]),
                "address": address, "topics": [selectors[0] if len(selectors) == 1 else selectors]}
        logs.extend(request(endpoint, message("eth_getLogs", [filt], 1))["result"])
    return project(start, end, address, selectors, headers, logs)


def project(start, end, address, selectors, headers, logs):
    blocks = [{key: block[key] for key in BLOCK_FIELDS} for block in headers]
    if [int(b["number"], 16) for b in blocks] != list(range(start, end + 1)):
        raise ValueError("incomplete block range")
    if any(b["parentHash"] != a["hash"] for a, b in zip(blocks, blocks[1:])):
        raise ValueError("disconnected headers")
    rows = sorted(({key: log[key] for key in LOG_FIELDS} for log in logs),
                  key=lambda row: (int(row["blockNumber"], 16), int(row["logIndex"], 16)))
    hashes = {b["number"]: b["hash"] for b in blocks}
    seen = set()
    for row in rows:
        key = (row["blockHash"], int(row["logIndex"], 16))
        if (row["removed"] or hashes.get(row["blockNumber"]) != key[0] or key in seen
                or row["topics"][0] not in selectors or row["address"].lower() != address.lower()):
            raise ValueError(f"unexpected canonical log {key}")
        seen.add(key)
    return {"blocks": blocks, "logs": rows}


def encoded(chains):
    return json.dumps(chains, separators=(",", ":"), sort_keys=True).encode()


def run(directory, contract, selectors):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=pathlib.Path, help="write a new archive at this path")
    args = parser.parse_args()
    config = json.loads((directory / "config.json").read_text())
    declaration = config["contracts"][contract]
    networks = declaration["chain"]
    if isinstance(networks, str):
        networks = {networks: {}}
    chains = {}
    for name, override in networks.items():
        chain = config["chains"][name]
        source = declaration | override
        chains[name] = capture(os.environ[f"PONDER_RPC_URL_{chain['id']}"],
                               source["startBlock"], source["endBlock"], source["address"], selectors)
    data = encoded(chains)
    if args.output is None:
        if data != gzip.decompress((directory / "archive.json.gz").read_bytes()):
            raise SystemExit("archive node returned different block or log data")
        print("All headers and logs match the checked-in archive.")
    else:
        with args.output.open("xb") as output:
            output.write(gzip.compress(data, mtime=0))
