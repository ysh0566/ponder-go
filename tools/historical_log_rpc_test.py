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
            return {"number": params[0], "hash": self.block}
        if method == "eth_getTransactionReceipt":
            return {"blockHash": self.block, "logs": [self.log]}
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
        for failure in ["chain", "receipt", "anchor", "duplicate", "foreign", "wrong height"]:
            seen_end = False

            def bad(url, method, params):
                nonlocal seen_end
                v = self.upstream(url, method, params)
                if method == "eth_chainId" and failure == "chain":
                    return "0x2"
                if method == "eth_getTransactionReceipt" and failure == "receipt":
                    return {**v, "logs": []}
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
