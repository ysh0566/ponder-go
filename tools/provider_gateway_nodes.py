"""Start OP Labs proxyd in front of the captured Base archive and a Swell development chain."""
from contextlib import closing
import socket
import subprocess
import time

from historical_log_rpc import rpc


def available_port():
    with closing(socket.socket()) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def start_process(args, output, name, stack):
    log = stack.enter_context((output / (name + ".log")).open("w"))
    process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
    def stop():
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
    stack.callback(stop)
    return process


def ready(process, port, chain):
    url = f"http://127.0.0.1:{port}"
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError(f"{process.args[0]} exited during startup")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                break
        except OSError:
            time.sleep(0.05)
    if int(rpc(url, "eth_chainId", []), 16) != chain:
        raise RuntimeError(f"unexpected chain on {url}")
    return url


def start_gateways(proxyd, anvil, base_backend, output, stack):
    anvil_port = available_port()
    chain = start_process([anvil.resolve(), "--host", "127.0.0.1", "--port", str(anvil_port),
                           "--chain-id", "1923", "--number", "6003001", "--accounts", "0", "--silent"], output, "swell-anvil", stack)
    swell_backend = ready(chain, anvil_port, 1923)
    gateways = {}
    for name, chain_id, backend, timeout, max_range in (
        ("base", 8453, base_backend, 2, 1000000), ("swell", 1923, swell_backend, 10, 1000)
    ):
        port = available_port()
        config = output / (name + "-proxyd.toml")
        config.write_text(f'''[server]
rpc_host = "127.0.0.1"
rpc_port = {port}
max_body_size_bytes = 65536
max_concurrent_rpcs = 16

[backend]
response_timeout_seconds = {timeout}
max_response_size_bytes = 33554432
max_retries = 0

[backends.local]
rpc_url = "{backend}"

[backend_groups.local]
backends = ["local"]
max_block_range = {max_range}

[rpc_method_mappings]
eth_chainId = "local"
eth_getLogs = "local"
''')
        proxy = start_process([proxyd.resolve(), config], output, name+"-proxyd", stack)
        gateways[name] = ready(proxy, port, chain_id)
    return gateways
