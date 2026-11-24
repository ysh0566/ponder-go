import gzip
from io import BytesIO
import json
from pathlib import Path
from tempfile import TemporaryDirectory
from threading import Thread
import unittest
from unittest.mock import patch
from urllib.request import Request, urlopen

import historical_log_rpc as archive


class HistoricalLogTest(unittest.TestCase):
    def setUp(self):
        temp = TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.output = Path(temp.name) / "history"
        self.address = "0x" + "12" * 20
        self.block = "0x" + "aa" * 32
        self.tx = "0x" + "bb" * 32
        self.log = dict(blockHash=self.block, blockNumber="0x2", transactionHash=self.tx,
                        logIndex="0x0", transactionIndex="0x1", address=self.address,
                        topics=[], data="0x", removed=False)

    def upstream(self, url, method, params):
        if method == "eth_chainId":
            return "0x1"
        if method == "eth_getBlockByNumber":
            return {"number": params[0], "hash": self.block, "parentHash": self.tx}
        if method == "eth_getTransactionReceipt":
            tx = "0x" + self.tx[2:].upper()
            return dict(blockHash=self.block, blockNumber="0x2", transactionHash=tx, transactionIndex="0x1",
                        logs=[{**self.log, "transactionHash": tx}])
        self.assertEqual(method, "eth_getLogs")
        q = params[0]
        return [self.log] if int(q["fromBlock"], 16) <= 2 <= int(q["toBlock"], 16) else []

    def collect(self, upstream=None, address=None):
        with patch.object(archive, "rpc", side_effect=upstream or self.upstream):
            archive.collect("http://localhost", 1, 1, 3, 1, address, self.output)
        return archive.Index(self.output)

    def test_complete_range_and_filtering_over_http(self):
        index = self.collect()
        http = archive.server([index], 0)
        self.addCleanup(http.server_close)
        thread = Thread(target=http.serve_forever)
        thread.start()
        self.addCleanup(thread.join)
        self.addCleanup(http.shutdown)
        url = f"http://127.0.0.1:{http.server_port}"

        def request(first, last, **extra):
            return archive.rpc(url, "eth_getLogs", [{"fromBlock": first, "toBlock": last, **extra}])

        self.assertEqual(archive.rpc(url, "eth_chainId", []), "0x1")
        self.assertEqual(request("0x1", "0x3"), [self.log])
        self.assertEqual(request("0x2", "0x2", address=self.address), [self.log])
        self.assertEqual(request("0x1", "0x1"), [])
        self.assertEqual(request("0x1", "0x3", address="0x" + "ff" * 20), [])
        for args in [("0x0", "0x3"), ("0x2", "latest"), ("0x3", "0x1")]:
            with self.assertRaises(ValueError):
                request(*args)
        with self.assertRaises(ValueError):
            request("0x1", "0x3", topics=[])

        archive.page_file(self.output, 2, 2).write_bytes(b"[]")
        payload = dict(jsonrpc="2.0", id=2, method="eth_getLogs", params=[{"fromBlock":"0x2", "toBlock":"0x2"}])
        with urlopen(Request(url, data=json.dumps(payload).encode())) as response:
            self.assertEqual(json.load(response)["error"]["code"], -32603)

    def test_filtered_index_cannot_answer_unfiltered_or_overlapping_queries(self):
        index = self.collect(address=self.address)
        query = {"fromBlock": "0x1", "toBlock": "0x3"}
        with self.assertRaises(ValueError):
            archive.get_logs([index], [query])
        with self.assertRaises(ValueError):
            archive.server([index, index], 0)
        with self.assertRaises(FileExistsError):
            self.collect()

    def test_reject_page_gap_and_changed_payload(self):
        self.collect()
        f = self.output / "manifest.json"
        m = json.loads(f.read_bytes())
        for pages in [m["pages"][:1], m["pages"][1:], m["pages"] + m["pages"][-1:]]:
            f.write_text(json.dumps({**m, "pages": pages}))
            with self.assertRaises(ValueError):
                archive.Index(self.output)
        f.write_text(json.dumps(m))
        archive.page_file(self.output, 2, 2).write_bytes(b"[]")
        with self.assertRaises(OSError):
            archive.Index(self.output)

    def test_collection_publishes_only_verified_pages(self):
        for failure in ["chain", "receipt", "wrong receipt transaction", "anchor", "duplicate", "foreign", "wrong height"]:
            seen_end = False

            def bad(url, method, params):
                nonlocal seen_end
                v = self.upstream(url, method, params)
                if method == "eth_chainId" and failure == "chain":
                    return "0x2"
                if method == "eth_getTransactionReceipt" and failure == "receipt":
                    return {**v, "logs": []}
                if method == "eth_getTransactionReceipt" and failure == "wrong receipt transaction":
                    return {**v, "transactionHash": self.block}
                if method == "eth_getBlockByNumber" and params[0] == "0x3":
                    if failure == "anchor" and seen_end:
                        return {**v, "hash": self.tx}
                    seen_end = True
                if method == "eth_getLogs" and v:
                    if failure == "duplicate":
                        return v + v
                    if failure == "foreign":
                        return [{**v[0], "address": "0x" + "ff" * 20}]
                    if failure == "wrong height":
                        return [{**v[0], "blockNumber": "0x3"}]
                return v

            with self.subTest(failure=failure), self.assertRaises(ValueError):
                self.collect(bad, self.address)
            self.assertFalse(self.output.exists())

    def test_capture_and_replay_observed_rpc_error(self):
        index = self.collect()
        capture = self.output.parent / "error.json"
        failure = {"code": -32603, "message": "historical range limit", "data": {"limit": 1}}
        calls = []

        def upstream(url, method, params):
            calls.append(method)
            reply = {"jsonrpc": "2.0", "id": 1}
            if method == "eth_getLogs":
                reply["error"] = failure
            else:
                reply["result"] = self.upstream(url, method, params)
            return reply

        with patch.object(archive, "rpc_response", side_effect=upstream):
            archive.capture_error("https://origin.example/private-path", 1, 1, 3, self.address, capture)
        self.assertEqual(calls, ["eth_chainId", "eth_getBlockByNumber", "eth_getLogs", "eth_getLogs", "eth_getBlockByNumber"])
        record = json.loads(capture.read_bytes())
        self.assertEqual(record["source"], "origin.example")
        self.assertEqual(record["anchor"], self.block)
        self.assertEqual(record["parentHash"], self.tx)
        self.assertEqual(record["error"], failure)
        with self.assertRaises(FileExistsError):
            archive.capture_error("https://origin.example", 1, 1, 3, self.address, capture)

        for incompatible in [[], {**record, "source": 1}, {**record, "anchor": self.tx},
                             {**record, "params": [{"fromBlock": "0x1", "toBlock": "0x4"}]},
                             {**record, "chain": 2}, {**record, "method": "eth_sendTransaction"},
                             {**record, "error": {"code": True, "message": "wrong type"}}]:
            with self.assertRaises(ValueError):
                archive.server([index], 0, [incompatible])
        with self.assertRaises(ValueError):
            archive.server([index], 0, [record, record])

        with archive.server([index], 0, [record]) as http:
            thread = Thread(target=http.serve_forever)
            thread.start()
            try:
                url = f"http://127.0.0.1:{http.server_port}"
                query = {"fromBlock": "0x1", "toBlock": "0x3", "address": self.address}
                payload = {"jsonrpc": "2.0", "id": "original-id", "method": "eth_getLogs", "params": [query]}
                with urlopen(Request(url, data=json.dumps(payload).encode())) as response:
                    self.assertEqual(json.load(response), {"jsonrpc": "2.0", "id": "original-id", "error": failure})
                self.assertEqual(archive.rpc(url, "eth_getLogs", [{**query, "toBlock": "0x2"}]), [self.log])
                self.assertEqual(archive.rpc(url, "eth_getLogs", [{"fromBlock": "0x1", "toBlock": "0x3"}]), [self.log])
            finally:
                http.shutdown()
                thread.join()

    def test_invalid_rpc_envelopes_cannot_be_recorded(self):
        normal = {"jsonrpc": "2.0", "id": 1}
        error = {"code": -32602, "message": "range limit"}
        for reply in [[], normal, {**normal, "error": error, "result": []},
                      {**normal, "id": True, "error": error}, {**normal, "id": "1", "error": error},
                      {**normal, "jsonrpc": "1.0", "error": error}]:
            with self.subTest(reply=reply), patch.object(archive, "urlopen", return_value=BytesIO(json.dumps(reply).encode())):
                with self.assertRaises(ValueError):
                    archive.rpc_response("http://localhost", "eth_getLogs", [])

    def test_error_capture_rejects_success_instability_and_transport_failure(self):
        output = self.output.parent / "never-published.json"
        error = {"error": {"code": -32603, "message": "range limit"}}
        chain = {"result": "0x1"}
        header = {"result": {"number": "0x3", "hash": self.block, "parentHash": self.tx}}
        changed = {"result": {"number": "0x3", "hash": self.tx, "parentHash": self.block}}
        for replies in [
            [{"result": "0x2"}],
            [chain, header, {"result": []}],
            [chain, header, OSError("network unavailable")],
            [chain, header, error, {"error": {"code": -32602, "message": "different"}}],
            [chain, header, error, error, changed],
        ]:
            with self.subTest(replies=replies), patch.object(archive, "rpc_response", side_effect=replies):
                with self.assertRaises((ValueError, OSError)):
                    archive.capture_error("https://origin.example", 1, 1, 3, None, output)
                self.assertFalse(output.exists())

    def test_block_receipt_lookup_checks_transaction_block_and_log(self):
        direct = self.upstream(None, "eth_getTransactionReceipt", [self.tx])
        for failure in ["absent", "duplicate", "wrong transaction", "wrong block", "different data", None]:
            def upstream(url, method, params):
                if method == "eth_getTransactionReceipt":
                    return None
                if method == "eth_getBlockReceipts":
                    self.assertEqual(params, [self.log["blockNumber"]])
                    if failure == "absent":
                        return None
                    if failure == "duplicate":
                        return [direct, direct]
                    if failure == "wrong transaction":
                        return [{**direct, "transactionHash": self.block}]
                    if failure == "wrong block":
                        return [{**direct, "blockHash": self.tx}]
                    if failure == "different data":
                        return [{**direct, "logs": [{**self.log, "data": "0xff"}]}]
                    return [direct]
                return self.upstream(url, method, params)

            with self.subTest(failure=failure), patch.object(archive, "rpc", side_effect=upstream):
                if failure:
                    with self.assertRaises(ValueError):
                        archive.check_receipt("http://localhost", self.log)
                else:
                    archive.check_receipt("http://localhost", self.log)

    def test_observed_logs_check_pages_and_serve_only_the_captured_filter(self):
        output = self.output.parent / "logs.json.gz"
        with patch.object(archive, "rpc", side_effect=self.upstream) as remote:
            archive.capture_logs("https://archive.example/key", 1, 1, 3, 2, self.address, output)
        self.assertEqual([c.args[1] for c in remote.call_args_list],
                         ["eth_chainId", "eth_getLogs", "eth_getLogs", "eth_getLogs", "eth_chainId"])
        record = json.loads(gzip.decompress(output.read_bytes()))
        self.assertEqual(record["source"], "archive.example")
        self.assertEqual(record["result"], [self.log])
        self.assertEqual(record["pageWidth"], 2)
        with self.assertRaises(FileExistsError):
            archive.capture_logs("http://localhost", 1, 1, 3, 2, self.address, output)

        with archive.server([], 0, log_captures=[record]) as http:
            thread = Thread(target=http.serve_forever)
            thread.start()
            try:
                url = f"http://127.0.0.1:{http.server_port}"
                query = record["params"][0]
                self.assertEqual(archive.rpc(url, "eth_chainId", []), "0x1")
                self.assertEqual(archive.rpc(url, "eth_getLogs", [query]), [self.log])
                for other in [{**query, "fromBlock": "0x2"}, {**query, "toBlock": "0x4"},
                              {k: v for k, v in query.items() if k != "address"}, {**query, "topics": []}]:
                    with self.assertRaises(ValueError):
                        archive.rpc(url, "eth_getLogs", [other])
            finally:
                http.shutdown()
                thread.join()
        for records in [[record, record], [{**record, "result": []}], [{**record, "pageWidth": False}],
                        [{**record, "error": {}}], [record, {**record, "chain": 2, "params": [
                            {"fromBlock": "0x1", "toBlock": "0x2", "address": self.address}]}]]:
            with self.subTest(records=records), self.assertRaises(ValueError):
                archive.server([], 0, log_captures=records)
        index = self.collect()
        with self.assertRaises(ValueError):
            archive.server([index], 0, log_captures=[record])

    def test_invalid_or_inconsistent_log_observations_are_never_published(self):
        path = self.output.parent / "invalid.json.gz"
        for change in ["missing", "different data", "malformed", "unordered", "changed chain", "wrong chain"]:
            def remote(url, method, params):
                answer = self.upstream(url, method, params)
                if method == "eth_chainId":
                    if change == "wrong chain" or (change == "changed chain" and len(traces) > 1):
                        return "0x2"
                elif answer and params[0]["toBlock"] == "0x2":
                    if change == "missing":
                        return []
                    if change == "different data":
                        return [{**answer[0], "data": "0xff"}]
                    if change == "malformed":
                        return [{**answer[0], "blockHash": "0xff"}]
                    if change == "unordered":
                        return answer + answer
                traces.append(method)
                return answer

            traces = []
            with self.subTest(change=change), patch.object(archive, "rpc", side_effect=remote), self.assertRaises(ValueError):
                archive.capture_logs("https://public.example", 1, 1, 3, 2, self.address, path)
            self.assertFalse(path.exists())
