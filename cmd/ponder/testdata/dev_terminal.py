"""Drive the built CLI and generated indexer through a real pseudo-terminal."""
import contextlib
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import termios
import time
import urllib.request

binary, root, prefix = sys.argv[1:]
source = Path(root, "main.go")
good = source.read_text().replace('package main', 'package main\nimport "time"', 1).replace('func main() {', 'func main() {\nfmt.Print("child-"); time.Sleep(80 * time.Millisecond); fmt.Println("progress-log")')
source.write_text(good)


@contextlib.contextmanager
def launch(*args, redirect=False):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 90, 0, 0))
    proc = None
    try:
        proc = subprocess.Popen(
            [binary, "dev", "--root", root, "--port", "0", "--schema", prefix, *args],
            env={**os.environ, "TERM": "xterm", "PONDER_HOSTNAME": "127.0.0.1"},
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL if redirect else slave,
            stderr=slave, start_new_session=True,
        )
        def until(pattern):
            transcript = bytearray()
            deadline = time.monotonic() + 40
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.25)[0]:
                    transcript.extend(os.read(master, 65536))
                    if match := re.search(pattern, transcript):
                        return bytes(transcript), match
                if proc.poll() is not None:
                    break
            raise AssertionError((pattern, bytes(transcript[-900:])))
        yield until
        proc.send_signal(signal.SIGINT)
        assert proc.wait(timeout=15) == 0
        if not args and not redirect:
            os.set_blocking(master, False)
            tail = bytearray()
            while select.select([master], [], [], 0)[0]:
                tail.extend(os.read(master, 65536))
            assert tail.endswith(b"\x1b[J"), bytes(tail[-90:])
    finally:
        if proc and proc.poll() is None:
            os.killpg(proc.pid, signal.SIGKILL)
            proc.wait()
        os.close(master)
        os.close(slave)


with launch() as read:
    output, match = read(rb"Ready \| http://(127\.0\.0\.1:\d+)\s+local  block 0")
    assert b'Ponder dev | Building' in output and b'\x1b[J' in output
    assert b'child-progress-log' in output
    base = "http://" + match[1].decode()
    mine = urllib.request.Request(os.environ["PONDER_LOCAL_RPC_URL"],
          data=b'{"jsonrpc":"2.0","id":1,"method":"evm_mine","params":[]}',
          headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(mine, timeout=5) as result:
        assert "result" in json.load(result)
    read(rb"local  block 1")
    source.write_text("broken")
    read(rb"Ponder dev \| Build failed, previous indexer still running")
    with urllib.request.urlopen(base + "/status", timeout=5) as result:
        assert json.load(result)["local"]["block"]["number"] == 1
    source.write_text(good + "\n// next revision\n")
    read(rb"Ready \| http://127\.0\.0\.1:\d+\s+local  block 1")
assert not list(Path(root, ".ponder-go", "bin").glob("dev-*"))

for flags, redirect in [(["--disable-ui"], False), (["--log-format=json"], False), ([], True)]:
    with launch(*flags, redirect=redirect) as read:
        output, _ = read(b"local example at http://")
        assert b"Ponder dev |" not in output and b"\x1b[" not in output
        assert not list(Path(root, ".ponder-go", "bin").glob("dev-*/progress.json"))
