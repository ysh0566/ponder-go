#!/usr/bin/env python3
"""Collect mined eth_getLogs pages and serve verified inclusive ranges on loopback."""
import argparse
from contextvars import ContextVar
import gzip
import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
from pathlib import Path
import re
import subprocess
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


NODE_FETCH = ContextVar("historical_rpc_node_fetch", default=False)
REQUEST_INTERVAL = ContextVar("historical_rpc_request_interval", default=0)


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


def fetch_rpc(request):
    if not NODE_FETCH.get():
        with urlopen(request, timeout=45) as response:
            return json.load(response)

    payload = json.dumps({"url": request.full_url, "body": request.data.decode()}).encode()
    try:
        response = subprocess.run(["node", str(Path(__file__).with_name("historical-rpc-fetch.mjs"))],
                                  input=payload, capture_output=True, timeout=50, check=True)
    except subprocess.TimeoutExpired as error:
        raise TimeoutError("RPC fetch timed out") from error
    except subprocess.CalledProcessError as error:
        if error.returncode == 124:
            raise TimeoutError("RPC fetch timed out") from error
        if error.returncode == 75:
            raise ConnectionResetError("RPC fetch connection closed") from error
        raise OSError(f"Node RPC fetch failed for {urlsplit(request.full_url).hostname}") from error
    metadata, _, body = response.stdout.partition(b"\n")
    status = json.loads(metadata)
    if not 200 <= status["status"] < 300:
        headers = {"Retry-After": status["retryAfter"]} if status["retryAfter"] else {}
        raise HTTPError(request.full_url, status["status"], "RPC request failed", headers, None)
    return json.loads(body)


def rpc_response(url, method, params):
    payload = json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)).encode()
    request = Request(url, data=payload, headers={"Content-Type": "application/json"})
    for attempt in range(3):
        if interval := REQUEST_INTERVAL.get():
            time.sleep(interval)
        try:
            result = fetch_rpc(request)
            break
        except HTTPError as error:
            delay = error.headers.get("Retry-After", str(2 ** (attempt + 1)))
            if error.code not in (429, 502, 503, 504) or attempt == 2 or not delay.isdigit() or int(delay) > 60:
                raise ValueError(f"{method} returned HTTP {error.code}") from None
            time.sleep(int(delay))
        except (ConnectionResetError, TimeoutError, URLError) as error:
            reason = error.reason if isinstance(error, URLError) else error
            if attempt == 2 or not isinstance(reason, (ConnectionResetError, TimeoutError)):
                raise
            time.sleep(2 ** (attempt + 1))
    if (not isinstance(result, dict) or type(result.get("id")) is not int or result["id"] != 1 or result.get("jsonrpc") != "2.0"
            or ("result" in result) == ("error" in result)):
        raise ValueError(f"{method} returned an invalid JSON-RPC response")
    return result


def rpc(url, method, params):
    result = rpc_response(url, method, params)
    if "error" in result:
        raise ValueError(f"{method} failed: {result.get('error')}")
    return result["result"]


def validate_logs(rows, first, last, address, chain=None):
    if not isinstance(rows, list):
        raise ValueError("expected an array of mined logs")
    previous = (-1, -1)
    previous_hash = previous_transaction = None
    for row in rows:
        height = quantity(row["blockNumber"])
        log_index = quantity(row["logIndex"])
        tx_index = quantity(row["transactionIndex"])
        tx_hash = fixed_hex(row["transactionHash"], 32)
        # Aurora receipts number logs within each transaction.
        pos = (height, tx_index, log_index) if chain == 1313161554 else (height, log_index)
        if chain == 1313161554 and previous[:2] == pos[:2] and previous_transaction != tx_hash:
            raise ValueError("different transactions at the same block position")
        block_hash = fixed_hex(row["blockHash"], 32)
        if not first <= height <= last or pos <= previous or row.get("removed", False) is not False:
            raise ValueError("log out of range, duplicated, unordered or removed")
        if previous[0] == height and block_hash != previous_hash:
            raise ValueError("different blocks at the same height")
        match = fixed_hex(row["address"], 20)
        if address is not None and match != address:
            raise ValueError("unexpected address in captured logs")
        if not isinstance(row["topics"], list) or len(row["topics"]) > 4:
            raise ValueError("invalid topics")
        for topic in row["topics"]:
            fixed_hex(topic, 32)
        if not re.fullmatch(r"0x(?:[0-9a-fA-F]{2})*", row["data"]):
            raise ValueError("invalid log data")
        previous, previous_hash, previous_transaction = pos, block_hash, tx_hash


def page_file(directory, first, last):
    return directory / f"{first}-{last}.json"


def block_header(url, height):
    block = rpc(url, "eth_getBlockByNumber", [hex(height), False])
    if not block or quantity(block["number"]) != height:
        raise ValueError(f"missing historical block {height}")
    return block


def block_hash(url, height):
    return fixed_hex(block_header(url, height)["hash"], 32)


def check_receipt(url, row):
    receipt = rpc(url, "eth_getTransactionReceipt", [row["transactionHash"]])
    if receipt is None:
        # Some archives retain block receipts after pruning their transaction-hash index.
        receipts = rpc(url, "eth_getBlockReceipts", [row["blockNumber"]])
        if not isinstance(receipts, list) or any(not isinstance(item, dict) for item in receipts):
            raise ValueError("missing historical block receipts")
        matches = [item for item in receipts
                   if fixed_hex(item.get("transactionHash"), 32) == row["transactionHash"].lower()]
        if len(matches) != 1:
            raise ValueError("expected one matching transaction in block receipts")
        receipt = matches[0]
    location = ("blockHash", "blockNumber", "transactionHash", "transactionIndex")
    def values(record, fields):
        return tuple(record[k].lower() for k in fields)

    def same_log(log):
        return (values(log, (*location, "logIndex", "address", "data")) ==
                values(row, (*location, "logIndex", "address", "data")) and
                [topic.lower() for topic in log["topics"]] == [topic.lower() for topic in row["topics"]])

    if (not receipt or values(receipt, location) != values(row, location) or
            receipt["blockHash"].lower() != block_hash(url, quantity(row["blockNumber"])) or
            not any(same_log(log) for log in receipt["logs"])):
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
    request_address = address
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
                query["address"] = request_address
            rows = rpc(url, "eth_getLogs", [query])
            validate_logs(rows, low, high, address, chain)
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
        self.anchor = fixed_hex(m["anchor"], 32)
        self.address = None if m["address"] is None else fixed_hex(m["address"], 20)
        self.pages = m["pages"]
        cursor = self.first
        for page in self.pages:
            low, high = page["first"], page["last"]
            if type(low) is not int or type(high) is not int or low != cursor or not low <= high <= self.last:
                raise ValueError("gap or overlap in archive pages")
            validate_logs(self.read(page), low, high, self.address, self.chain)
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


def filter_bounds(params):
    if not isinstance(params, list) or len(params) != 1 or not isinstance(params[0], dict):
        raise ValueError("expected one filter")
    query = params[0]
    if set(query) not in ({"fromBlock", "toBlock"}, {"fromBlock", "toBlock", "address"}):
        raise ValueError("expected explicit block bounds and an optional address")
    first, last = quantity(query["fromBlock"]), quantity(query["toBlock"])
    address = fixed_hex(query["address"], 20) if "address" in query else None
    if first > last:
        raise ValueError("reversed block bounds")
    return first, last, address


def get_logs(indexes, params):
    first, last, address = filter_bounds(params)
    for index in indexes:
        if index.first <= first <= last <= index.last and index.address in (None, address):
            return index.logs(first, last, address)
    raise ValueError("query outside captured historical range")


def check_rpc_error(error):
    if (not isinstance(error, dict) or type(error.get("code")) is not int
            or not isinstance(error.get("message"), str) or not error["message"]
            or not error.keys() <= {"code", "message", "data"}):
        raise ValueError("expected a JSON-RPC error object")
    return error


def capture_request(url, chain, first, last, address, output):
    source = urlsplit(url)
    if source.scheme not in ("http", "https") or not source.hostname:
        raise ValueError("expected an HTTP RPC endpoint")
    if first < 0 or first > last or chain < 1:
        raise ValueError("invalid chain or block bounds")
    if output.exists():
        raise FileExistsError(output)
    query = {"fromBlock": hex(first), "toBlock": hex(last)}
    if address is not None:
        query["address"] = fixed_hex(address, 20)
    if quantity(rpc(url, "eth_chainId", [])) != chain:
        raise ValueError("wrong RPC chain")

    return source.hostname, query


def capture_error(url, chain, first, last, address, output, header_url=None):
    host, query = capture_request(url, chain, first, last, address, output)

    if header_url is not None:
        source = urlsplit(header_url)
        if source.scheme not in ("https", "http") or not source.hostname:
            raise ValueError("expected an HTTP header RPC endpoint")
        if quantity(rpc(header_url, "eth_chainId", [])) != chain:
            raise ValueError("wrong header RPC chain")

    def anchors():
        header = block_header(header_url or url, last)
        return fixed_hex(header["hash"], 32), fixed_hex(header["parentHash"], 32)

    anchor, parent = anchors()
    params = [query]
    error = check_rpc_error(rpc_response(url, "eth_getLogs", params).get("error"))
    repeated = check_rpc_error(rpc_response(url, "eth_getLogs", params).get("error"))
    if error != repeated or anchors() != (anchor, parent):
        raise ValueError("error or historical block changed during capture")
    record = dict(version=1, chain=chain, source=host, anchor=anchor, parentHash=parent,
                  method="eth_getLogs", params=params, error=error)
    if header_url is not None:
        record["headerSource"] = source.hostname
    publish_capture(output, (json.dumps(record, indent=2) + "\n").encode())


def publish_capture(output, payload):
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=output.parent) as temporary:
        ready = Path(temporary) / "capture"
        ready.write_bytes(payload)
        output.hardlink_to(ready)


def log_digest(rows):
    return digest(json.dumps(rows, sort_keys=True, separators=(",", ":")).encode())


def capture_logs(url, chain, first, last, width, address, output):
    if width < 1:
        raise ValueError("invalid page width")
    host, query = capture_request(url, chain, first, last, address, output)
    address = query.get("address")
    rows = rpc(url, "eth_getLogs", [query])
    validate_logs(rows, first, last, address, chain)
    offset = 0
    for low in range(first, last + 1, width):
        high = min(last, low + width - 1)
        subquery = {**query, "fromBlock": hex(low), "toBlock": hex(high)}
        page = rpc(url, "eth_getLogs", [subquery])
        validate_logs(page, low, high, address, chain)
        if log_digest(page) != log_digest(rows[offset:offset + len(page)]):
            raise ValueError("paged response differs from the full log response")
        offset += len(page)
    if offset != len(rows) or quantity(rpc(url, "eth_chainId", [])) != chain:
        raise ValueError("log count or chain changed during capture")
    record = dict(version=1, chain=chain, source=host, method="eth_getLogs", params=[query],
                  pageWidth=width, resultSha256=log_digest(rows), result=rows)
    publish_capture(output, gzip.compress(json.dumps(record, separators=(",", ":")).encode(), mtime=0))


def capture_filter(record):
    if (not isinstance(record, dict) or type(record.get("version")) is not int or record["version"] != 1
            or type(record.get("chain")) is not int or record["chain"] < 1 or record.get("method") != "eth_getLogs"
            or not isinstance(record.get("source"), str) or not record["source"].strip()):
        raise ValueError("incompatible historical RPC capture")
    return filter_bounds(record.get("params"))


def range_policy_error(url, chain, params):
    first, last, _ = filter_bounds(params)
    reply = rpc_response(url, "eth_getLogs", params)
    if "result" in reply:
        if not isinstance(reply["result"], list):
            raise ValueError("range policy node returned invalid logs")
        return None
    error = check_rpc_error(reply["error"])
    if chain == 239 and error["code"] == -32000:
        # A TAC validator admits the range before looking up local history.
        # Only a missing, below-genesis height can fall through to the archive.
        miss = re.fullmatch(
            r"failed to fetch block result from CometBFT: failed to fetch block result from CometBFT "
            r"(\d+): height \1 is not available, lowest height is (\d+)", error["message"]
        )
        if miss and first <= int(miss[1]) <= last and int(miss[1]) < int(miss[2]):
            return None
    return error


def server(indexes, port, error_captures=(), log_captures=(), bytes_per_second=None, range_policy_url=None):
    if bytes_per_second is not None and bytes_per_second <= 0:
        raise ValueError("bytes per second must be positive")
    chains = {index.chain for index in indexes}
    observed = {}
    for record in log_captures:
        key = capture_filter(record)
        chains.add(record["chain"])
        rows = record.get("result")
        validate_logs(rows, *key, chain=record["chain"])
        if (type(record.get("pageWidth")) is not int or record["pageWidth"] < 1 or "error" in record
                or log_digest(rows) != record.get("resultSha256")):
            raise ValueError("invalid observed log response")
        if key in observed:
            raise ValueError("duplicate observed log filter")
        if any(idx.first <= key[0] <= key[1] <= idx.last and idx.address in (None, key[2]) for idx in indexes):
            raise ValueError("observed log filter already covered by an archive index")
        observed[key] = {"result": rows}
    if len(chains) != 1:
        raise ValueError("one chain per listener is required")
    chain = chains.pop()
    if range_policy_url is not None:
        endpoint = urlsplit(range_policy_url)
        if endpoint.scheme not in ("http", "https") or endpoint.hostname not in ("localhost", "127.0.0.1", "::1"):
            raise ValueError("range policy node must run on loopback")
        local_chain = {43114: 43112, 239: 2391337}.get(chain)
        if local_chain is None or quantity(rpc(range_policy_url, "eth_chainId", [])) != local_chain:
            raise ValueError("range policy node does not match the archive network")
    for i, index in enumerate(indexes):
        for other in indexes[:i]:
            if (index.first <= other.last and other.first <= index.last and
                    (index.address == other.address or None in (index.address, other.address))):
                raise ValueError("ambiguous overlapping indexes")
    for capture in error_captures:
        key = capture_filter(capture)
        if capture["chain"] != chain or "result" in capture:
            raise ValueError("incompatible historical error capture")
        anchors = {key[1]: fixed_hex(capture.get("anchor"), 32),
                   key[1] - 1: fixed_hex(capture.get("parentHash"), 32)}
        if any(index.last in anchors and index.anchor != anchors[index.last] for index in indexes):
            raise ValueError("historical error and archive disagree on the block hash")
        if key in observed:
            raise ValueError("duplicate historical error capture")
        observed[key] = {"error": check_rpc_error(capture.get("error"))}

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
                        result["result"] = hex(chain)
                    elif req["method"] == "eth_getLogs":
                        params = req.get("params")
                        policy_error = range_policy_error(range_policy_url, chain, params) if range_policy_url else None
                        if policy_error is not None:
                            result["error"] = policy_error
                        elif observed and (key := filter_bounds(params)) in observed:
                            result.update(observed[key])
                        else:
                            result["result"] = get_logs(indexes, params)
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
            try:
                if bytes_per_second is None:
                    self.wfile.write(raw)
                else:
                    for start in range(0, len(raw), 16384):
                        chunk = raw[start:start+16384]
                        time.sleep(len(chunk) / bytes_per_second)
                        self.wfile.write(chunk)
            except (BrokenPipeError, ConnectionResetError):
                return

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
    error = commands.add_parser("capture-error")
    error.add_argument("--rpc-url", required=True)
    error.add_argument("--header-rpc-url", help="same-chain archive for headers when the error source lacks historical blocks")
    for name in ("chain", "first", "last"):
        error.add_argument("--" + name, type=int, required=True)
    error.add_argument("--address")
    error.add_argument("output", type=Path)
    logs = commands.add_parser("capture-logs")
    logs.add_argument("--rpc-url", required=True)
    for name in ("chain", "first", "last", "page-width"):
        logs.add_argument("--" + name, type=int, required=True)
    logs.add_argument("--address")
    logs.add_argument("output", type=Path)
    host = commands.add_parser("serve")
    host.add_argument("--port", type=int, default=0)
    host.add_argument("--range-policy-url", help="apply a local Coreth or TAC node’s range policy before reading archived logs")
    host.add_argument("--bytes-per-second", type=int, help="limit the transfer rate of all response bodies")
    host.add_argument("--error-capture", action="append", type=Path, default=[])
    host.add_argument("--log-capture", action="append", type=Path, default=[])
    host.add_argument("directories", type=Path, nargs="*")
    for collecting in (capture, error, logs):
        collecting.add_argument("--node-fetch", action="store_true", help="use the upstream Node Fetch HTTP client")
        collecting.add_argument("--request-interval", type=int, default=0,
                                help="pause this many seconds before each remote read, including retries")
    args = parser.parse_args()
    interval = getattr(args, "request_interval", 0)
    if interval < 0:
        parser.error("--request-interval must be nonnegative")
    fetch_token = NODE_FETCH.set(getattr(args, "node_fetch", False))
    token = REQUEST_INTERVAL.set(interval)
    try:
        run_command(args)
    finally:
        REQUEST_INTERVAL.reset(token)
        NODE_FETCH.reset(fetch_token)


def run_command(args):
    if args.command == "collect":
        collect(args.rpc_url, args.chain, args.first, args.last, args.page_width, args.address, args.directory)
    elif args.command == "capture-logs":
        capture_logs(args.rpc_url, args.chain, args.first, args.last, args.page_width, args.address, args.output)
    elif args.command == "capture-error":
        capture_error(args.rpc_url, args.chain, args.first, args.last, args.address, args.output, args.header_rpc_url)
    else:
        captures = [json.loads(path.read_bytes()) for path in args.error_capture]
        responses = [json.loads(gzip.decompress(path.read_bytes())) for path in args.log_capture]
        with server([Index(path) for path in args.directories], args.port, captures, responses, args.bytes_per_second, args.range_policy_url) as http:
            print(f"http://127.0.0.1:{http.server_port}", flush=True)
            http.serve_forever()


if __name__ == "__main__":
    main()
