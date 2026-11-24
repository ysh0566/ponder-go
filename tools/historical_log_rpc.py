#!/usr/bin/env python3
"""Collect mined eth_getLogs pages and serve verified inclusive ranges on loopback."""
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
from pathlib import Path
import re
import tempfile
import time
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


def quantity(value):
    if not isinstance(value, str) or not re.fullmatch(r"0x(?:0|[1-9a-fA-F][0-9a-fA-F]*)", value):
        raise ValueError("expected an explicit hexadecimal block or index")
    return int(value, 16)


def fixed_hex(value, size):
    if not isinstance(value, str) or not re.fullmatch(r"0x[0-9a-fA-F]{%d}" % (size * 2), value):
        raise ValueError(f"expected {size} bytes of hex")
    return value.lower()


def digest(data):
    return hashlib.sha256(data).hexdigest()


def rpc(url, method, params):
    payload = json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)).encode()
    request = Request(url, data=payload, headers={"Content-Type": "application/json"})
    for attempt in range(3):
        try:
            with urlopen(request, timeout=45) as response:
                result = json.load(response)
            break
        except HTTPError as error:
            delay = error.headers.get("Retry-After", str(2 ** (attempt + 1)))
            if error.code not in (429, 502, 503, 504) or attempt == 2 or not delay.isdigit() or int(delay) > 60:
                raise ValueError(f"{method} returned HTTP {error.code}") from None
            time.sleep(int(delay))
    if result.get("id") != 1 or result.get("jsonrpc") != "2.0" or "result" not in result:
        raise ValueError(f"{method} failed: {result.get('error')}")
    return result["result"]


def validate_logs(rows, first, last, address):
    if not isinstance(rows, list):
        raise ValueError("expected an array of mined logs")
    previous = (-1, -1)
    previous_hash = None
    for row in rows:
        height = quantity(row["blockNumber"])
        pos = height, quantity(row["logIndex"])
        block_hash = fixed_hex(row["blockHash"], 32)
        if not first <= height <= last or pos <= previous or row.get("removed", False) is not False:
            raise ValueError("log out of range, duplicated, unordered or removed")
        if previous[0] == height and block_hash != previous_hash:
            raise ValueError("different blocks at the same height")
        match = fixed_hex(row["address"], 20)
        if address is not None and match != address:
            raise ValueError("unexpected address in captured logs")
        fixed_hex(row["transactionHash"], 32)
        quantity(row["transactionIndex"])
        if not isinstance(row["topics"], list) or len(row["topics"]) > 4:
            raise ValueError("invalid topics")
        for topic in row["topics"]:
            fixed_hex(topic, 32)
        if not re.fullmatch(r"0x(?:[0-9a-fA-F]{2})*", row["data"]):
            raise ValueError("invalid log data")
        previous, previous_hash = pos, block_hash


def page_file(directory, first, last):
    return directory / f"{first}-{last}.json"


def block_hash(url, height):
    block = rpc(url, "eth_getBlockByNumber", [hex(height), False])
    if not block or quantity(block["number"]) != height:
        raise ValueError(f"missing historical block {height}")
    return fixed_hex(block["hash"], 32)


def check_receipt(url, row):
    receipt = rpc(url, "eth_getTransactionReceipt", [row["transactionHash"]])
    fields = ("blockHash", "blockNumber", "transactionHash", "transactionIndex", "logIndex", "address", "topics", "data")
    if (not receipt or receipt["blockHash"] != block_hash(url, quantity(row["blockNumber"])) or
            not any(all(log[k] == row[k] for k in fields) for log in receipt["logs"])):
        raise ValueError("historical log does not match its canonical receipt")


def collect(url, chain, first, last, width, address, output):
    origin = urlsplit(url)
    if origin.scheme not in ("https", "http") or not origin.hostname:
        raise ValueError("expected an HTTP RPC endpoint")
    if not 0 <= first <= last or width < 1 or chain < 1:
        raise ValueError("invalid chain, bounds or page width")
    if output.exists():
        raise FileExistsError(output)
    if quantity(rpc(url, "eth_chainId", [])) != chain:
        raise ValueError("wrong RPC chain")
    address = fixed_hex(address, 20) if address is not None else None
    anchor = block_hash(url, last)
    manifest = dict(version=1, chain=chain, first=first, last=last, address=address,
                    anchor=anchor, source=origin.hostname, pages=[])
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=output.parent) as temp:
        stage = Path(temp) / "index"
        stage.mkdir()
        first_log = last_log = None
        for low in range(first, last + 1, width):
            high = min(last, low + width - 1)
            query = {"fromBlock": hex(low), "toBlock": hex(high)}
            if address:
                query["address"] = address
            rows = rpc(url, "eth_getLogs", [query])
            validate_logs(rows, low, high, address)
            if rows:
                first_log = first_log or rows[0]
                last_log = rows[-1]
            raw = json.dumps(rows, separators=(",", ":")).encode()
            page_file(stage, low, high).write_bytes(raw)
            manifest["pages"].append(dict(first=low, last=high, sha256=digest(raw)))
        if first_log:
            check_receipt(url, first_log)
            if first_log != last_log:
                check_receipt(url, last_log)
        if block_hash(url, last) != anchor:
            raise ValueError("range anchor changed during collection")
        (stage / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        stage.rename(output)


class Index:
    def __init__(self, directory):
        self.directory = directory
        m = json.loads((directory / "manifest.json").read_bytes())
        self.chain, self.first, self.last = m["chain"], m["first"], m["last"]
        if (m["version"] != 1 or any(type(n) is not int for n in (self.chain, self.first, self.last)) or
                self.chain < 1 or not 0 <= self.first <= self.last):
            raise ValueError("invalid archive bounds")
        fixed_hex(m["anchor"], 32)
        self.address = None if m["address"] is None else fixed_hex(m["address"], 20)
        self.pages = m["pages"]
        cursor = self.first
        for page in self.pages:
            low, high = page["first"], page["last"]
            if type(low) is not int or type(high) is not int or low != cursor or not low <= high <= self.last:
                raise ValueError("gap or overlap in archive pages")
            validate_logs(self.read(page), low, high, self.address)
            cursor = high + 1
        if cursor != self.last + 1:
            raise ValueError("archive does not cover its declared range")

    def read(self, page):
        raw = page_file(self.directory, page["first"], page["last"]).read_bytes()
        if digest(raw) != page["sha256"]:
            raise OSError("archive page checksum differs")
        return json.loads(raw)

    def logs(self, first, last, address):
        rows = []
        for page in self.pages:
            if page["last"] < first or page["first"] > last:
                continue
            rows.extend(row for row in self.read(page) if first <= quantity(row["blockNumber"]) <= last
                        and (address is None or row["address"].lower() == address))
        return rows


def get_logs(indexes, params):
    if not isinstance(params, list) or len(params) != 1 or not isinstance(params[0], dict):
        raise ValueError("expected one filter")
    query = params[0]
    if set(query) not in ({"fromBlock", "toBlock"}, {"fromBlock", "toBlock", "address"}):
        raise ValueError("expected explicit block bounds and an optional address")
    first, last = quantity(query["fromBlock"]), quantity(query["toBlock"])
    address = fixed_hex(query["address"], 20) if "address" in query else None
    for index in indexes:
        if index.first <= first <= last <= index.last and index.address in (None, address):
            return index.logs(first, last, address)
    raise ValueError("query outside captured historical range")


def server(indexes, port):
    if not indexes or len({index.chain for index in indexes}) != 1:
        raise ValueError("one chain per listener is required")
    for i, index in enumerate(indexes):
        for other in indexes[:i]:
            if (index.first <= other.last and other.first <= index.last and
                    (index.address == other.address or None in (index.address, other.address))):
                raise ValueError("ambiguous overlapping indexes")

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            result = {"jsonrpc": "2.0", "id": None}
            try:
                size = int(self.headers.get("Content-Length", "0"))
                if not 0 < size <= 65536:
                    self.send_error(400)
                    return
                req = json.loads(self.rfile.read(size))
                if not isinstance(req, dict) or req.get("jsonrpc") != "2.0" or not isinstance(req.get("method"), str):
                    result["error"] = dict(code=-32600, message="Invalid Request")
                else:
                    result["id"] = req.get("id")
                    if req["method"] == "eth_chainId":
                        if req.get("params", []) != []:
                            raise ValueError("eth_chainId takes no parameters")
                        result["result"] = hex(indexes[0].chain)
                    elif req["method"] == "eth_getLogs":
                        result["result"] = get_logs(indexes, req.get("params"))
                    else:
                        result["error"] = dict(code=-32601, message="Method not supported by historical log index")
            except (json.JSONDecodeError, UnicodeDecodeError):
                result["error"] = dict(code=-32700, message="Parse error")
            except ValueError as error:
                result["error"] = dict(code=-32602, message=str(error))
            except OSError:
                result["error"] = dict(code=-32603, message="Archive unavailable")
            raw = json.dumps(result, separators=(",", ":")).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

    return HTTPServer(("127.0.0.1", port), Handler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    capture = commands.add_parser("collect")
    capture.add_argument("--rpc-url", required=True)
    for name in ("chain", "first", "last", "page-width"):
        capture.add_argument("--" + name, type=int, required=True)
    capture.add_argument("--address")
    capture.add_argument("directory", type=Path)
    host = commands.add_parser("serve")
    host.add_argument("--port", type=int, default=0)
    host.add_argument("directories", type=Path, nargs="+")
    args = parser.parse_args()
    if args.command == "collect":
        collect(args.rpc_url, args.chain, args.first, args.last, args.page_width, args.address, args.directory)
    else:
        with server([Index(path) for path in args.directories], args.port) as http:
            print(f"http://127.0.0.1:{http.server_port}", flush=True)
            http.serve_forever()


if __name__ == "__main__":
    main()
