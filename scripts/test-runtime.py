#!/usr/bin/env python3
"""Real Linux binary, loopback panel and VLESS client. No production services."""
import argparse
import copy
import json
import os
from pathlib import Path
import signal
import socket
import socketserver
import struct
import subprocess
import tempfile
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def wait_for(check, timeout=12):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(0.1)
    raise AssertionError('runtime condition timed out')


def exact(conn, length):
    result = b''
    while len(result) < length:
        chunk = conn.recv(length - len(result))
        if not chunk:
            raise OSError('unexpected EOF')
        result += chunk
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary', type=Path)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    if os.name != 'posix':
        raise SystemExit('This test requires Linux SIGTERM semantics.')
    binary = args.binary.resolve()
    credential = uuid.UUID('ab2641a2-b9f3-4e33-b296-bba0ea55ac38')
    lock = threading.Lock()
    state = {'fail_push': True, 'users': [{'id': 101, 'uuid': str(credential)}],
             'reports': [], 'config_requests': 0}

    class Echo(socketserver.BaseRequestHandler):
        def handle(self):
            self.request.settimeout(5)
            try:
                while True:
                    data = self.request.recv(65536)
                    if not data:
                        return
                    self.request.sendall(data)
            except OSError:
                return

    class EchoServer(socketserver.ThreadingTCPServer):
        daemon_threads = True

    echo = EchoServer(('127.0.0.1', 0), Echo)
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        node_port = probe.getsockname()[1]

    class Panel(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            path = self.path.split('?', 1)[0]
            with lock:
                if path.endswith('/config'):
                    state['config_requests'] += 1
                    body = {'protocol': 'vless', 'server_port': node_port, 'network': 'tcp',
                            'tls': 0, 'base_config': {'pull_interval': 1, 'push_interval': 1}}
                elif path.endswith('/user'):
                    body = {'users': copy.deepcopy(state['users'])}
                elif path.endswith('/alivelist'):
                    body = {'alive': {}}
                else:
                    self.send_error(404)
                    return
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps(body).encode())

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            with lock:
                fail = state['fail_push'] and self.path.split('?', 1)[0].endswith('/push')
                if not fail and self.path.split('?', 1)[0].endswith('/push'):
                    state['reports'].append(body)
            self.send_response(503 if fail else 200)
            self.end_headers()
            self.wfile.write(b'{}')

    panel = ThreadingHTTPServer(('127.0.0.1', 0), Panel)
    for server in (echo, panel):
        threading.Thread(target=server.serve_forever, daemon=True).start()

    report = {'checks': []}
    try:
        with tempfile.TemporaryDirectory(prefix='v2bx-runtime-') as temp:
            root = Path(temp)
            config_path, log_path = root / 'config.json', root / 'runtime.log'
            document = {'Log': {'Level': 'error'}, 'Cores': [{'Type': 'xray'}], 'Nodes': [{
                'Core': 'xray', 'Name': 'runtime-loopback',
                'ApiHost': 'http://127.0.0.1:' + str(panel.server_port),
                'ApiKey': 'isolated-runtime-fixture', 'NodeID': 101, 'NodeType': 'vless',
                'ListenIP': '127.0.0.1', 'SendIP': '127.0.0.1', 'CertConfig': {'CertMode': 'none'}}]}

            def write_document(value):
                sibling = root / 'next-config.json'
                sibling.write_text(json.dumps(value), encoding='utf-8')
                os.replace(str(sibling), str(config_path))

            write_document(document)

            def exchange():
                with socket.create_connection(('127.0.0.1', node_port), timeout=2) as conn:
                    conn.settimeout(2)
                    payload = b'real-runtime-proxy-echo;' * 64
                    destination = struct.pack('!H', echo.server_address[1]) + b'\x01' + socket.inet_aton('127.0.0.1')
                    conn.sendall(b'\x00' + credential.bytes + b'\x00\x01' + destination + payload)
                    version, addons = exact(conn, 2)
                    assert version == 0
                    if addons:
                        exact(conn, addons)
                    assert exact(conn, len(payload)) == payload
                return True

            def reachable():
                try:
                    return exchange()
                except (OSError, AssertionError):
                    return False

            def log_contains(text):
                return text in log_path.read_text(encoding='utf-8', errors='replace')

            def pending():
                files = list((root / 'traffic').glob('*.json'))
                return files and bool(json.loads(files[0].read_text())['pending'])

            with log_path.open('wb') as output:
                process = subprocess.Popen([str(binary), 'server', '-c', str(config_path)],
                                           stdout=output, stderr=subprocess.STDOUT)
                try:
                    wait_for(reachable)
                    report['checks'].append('real VLESS authentication and bidirectional TCP echo')
                    wait_for(pending)
                    report['checks'].append('503 traffic report retained in on-disk journal')
                    process.send_signal(signal.SIGTERM)
                    assert process.wait(timeout=10) == 0
                    assert pending()
                    process = subprocess.Popen([str(binary), 'server', '-c', str(config_path)],
                                               stdout=output, stderr=subprocess.STDOUT)
                    wait_for(reachable)
                    assert pending()
                    report['checks'].append('pending traffic survives a real process restart')
                    with lock:
                        state['fail_push'] = False
                    wait_for(lambda: not pending())
                    with lock:
                        assert state['reports'] and all(set(row) == {'101'} for row in state['reports'])
                    report['checks'].append('traffic retry and persisted acknowledgement')

                    config_path.write_text('{', encoding='utf-8')
                    wait_for(lambda: log_contains('reload config rejected'))
                    assert reachable() and process.poll() is None
                    report['checks'].append('invalid JSON preserves the running listener')

                    invalid_runtime = copy.deepcopy(document)
                    invalid_runtime['Nodes'][0]['ListenIP'] = 'invalid-listen-address'
                    write_document(invalid_runtime)
                    wait_for(lambda: log_contains('previous runtime restored'))
                    wait_for(reachable)
                    assert process.poll() is None
                    report['checks'].append('failed runtime replacement restores the previous node')

                    updated = copy.deepcopy(document)
                    updated['Nodes'][0]['Name'] = 'runtime-reloaded'
                    write_document(updated)
                    wait_for(lambda: log_contains('reload config success'))
                    wait_for(reachable)
                    report['checks'].append('atomic config replacement applies and reconnects')

                    with lock:
                        state['users'] = []
                    wait_for(lambda: not reachable())
                    report['checks'].append('panel revocation rejects a new authenticated connection')
                    with lock:
                        state['users'] = [{'id': 101, 'uuid': str(credential)}]
                    wait_for(reachable)
                    report['checks'].append('panel reassignment restores access')

                    process.send_signal(signal.SIGTERM)
                    assert process.wait(timeout=10) == 0
                    report['checks'].append('SIGTERM performs a clean shutdown')
                    failed_start = subprocess.run([str(binary), 'server', '-c', str(root / 'missing.json')],
                                                  capture_output=True, timeout=10)
                    assert failed_start.returncode != 0
                    report['checks'].append('startup failure returns a nonzero process exit status')
                except BaseException:
                    if process.poll() is None:
                        process.terminate()
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait()
                    text = log_path.read_text(encoding='utf-8', errors='replace')
                    print(text[-5000:].replace('isolated-runtime-fixture', '[fixture]'))
                    raise
    finally:
        for server in (echo, panel):
            server.shutdown()
            server.server_close()
    report['passed'] = len(report['checks'])
    if args.report:
        args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
