"""Run the node-side ProbeServer from tcp_probe.py on an ephemeral loopback port.

Used by internal/probe/contract_test.go. tcp_probe.py itself is not vendored;
its path is the first argument. Target policy, framing and replies are the real
node implementation; only the final game connect is scripted by target port so
results never depend on the network:

    port 1 -> connected, 2 -> refused, 3 -> timeout, 4 -> unreachable,
    any other port -> the real tcp_connect.

Prints the bound port on the first line of stdout, then serves until killed.
"""

import errno
import importlib.util
import socket
import sys

spec = importlib.util.spec_from_file_location("tcp_probe", sys.argv[1])
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


def scripted_connect(address, port, timeout):
    if port == 1:
        return
    if port == 2:
        raise ConnectionRefusedError(errno.ECONNREFUSED, "scripted refusal")
    if port == 3:
        raise socket.timeout("scripted timeout")
    if port == 4:
        raise OSError(errno.EHOSTUNREACH, "scripted unreachable")
    probe.tcp_connect(address, port, timeout)


server = probe.ProbeServer("127.0.0.1", 0, connector=scripted_connect)
print(server.address[1], flush=True)
server.serve_forever()
