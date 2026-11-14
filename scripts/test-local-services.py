#!/usr/bin/env python3
"""Real binary/CLI/forwarding verification in an isolated test directory.

Run system-level protocol checks in a separate Linux network namespace.
This script touches only its --workdir and loopback listeners.
"""
import argparse
import json
from pathlib import Path
import signal
import socket
import subprocess
import threading
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--workdir', required=True)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    root = Path(args.workdir).resolve()
    root.mkdir(parents=True, exist_ok=True)
    config = root / 'config.json'
    config.write_text(json.dumps({'Cores': [], 'Nodes': [], 'LocalServiceDir': 'services'}))
    results = []
    processes = []
    handles = []
    echo = socket.socket()
    echo.bind(('127.0.0.1', 0))
    echo.listen()
    echo_port = echo.getsockname()[1]
    udp_echo = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    udp_echo.bind(('127.0.0.1', echo_port))

    def echo_one(conn):
        try:
            while True:
                data = conn.recv(65536)
                if not data:
                    break
                conn.sendall(data)
        finally:
            conn.close()

    def echo_tcp():
        while True:
            try:
                conn, _ = echo.accept()
            except OSError:
                return
            threading.Thread(target=echo_one, args=(conn,), daemon=True).start()

    def echo_udp():
        while True:
            try:
                data, peer = udp_echo.recvfrom(65536)
                udp_echo.sendto(data, peer)
            except OSError:
                return

    threading.Thread(target=echo_tcp, daemon=True).start()
    threading.Thread(target=echo_udp, daemon=True).start()
    reserve = socket.socket()
    reserve.bind(('127.0.0.1', 0))
    service_port = reserve.getsockname()[1]
    reserve.close()
    spec = {'ID': 'runtime-tunnel', 'Type': 'tunnel', 'Enabled': True,
            'Listen': f'127.0.0.1:{service_port}', 'Target': f'127.0.0.1:{echo_port}',
            'Network': 'tcp,udp'}
    spec_file = root / 'tunnel.input.json'

    def cli(*arguments, expect=0):
        run = subprocess.run([binary, 'services', '-c', str(config), *arguments],
                             capture_output=True, timeout=15)
        if run.returncode != expect:
            raise AssertionError(f'CLI exit {run.returncode}, expected {expect}: ' + run.stderr.decode(errors='replace'))
        return run.stdout

    def put(document):
        spec_file.write_text(json.dumps(document))
        cli('put', '-f', str(spec_file))

    def start():
        out = (root / f'server-{len(processes)}.log').open('wb')
        handles.append(out)
        p = subprocess.Popen([binary, 'server', '-c', str(config), '--watch=false'], stdout=out, stderr=out)
        processes.append(p)
        return p

    def status():
        rows = json.loads(cli('list', '--id', spec['ID']))
        return rows[0]['状态'] if rows else None

    def until(predicate, description):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if current.poll() is not None:
                raise AssertionError(description + ': server exited; ' +
                                     (root / f'server-{processes.index(current)}.log').read_text(errors='replace')[-4000:])
            if predicate():
                return
            time.sleep(.1)
        raise AssertionError(description)

    def payload():
        data = b'actual-local-service-transfer;' * 256
        with socket.create_connection(('127.0.0.1', service_port), timeout=2) as conn:
            conn.sendall(data)
            received = b''
            while len(received) < len(data):
                received += conn.recv(len(data) - len(received))
            assert received == data
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
            client.settimeout(3)
            client.sendto(data, ('127.0.0.1', service_port))
            assert client.recvfrom(65536)[0] == data

    try:
        assert json.loads(cli('list')) == []
        current = start()
        put(spec)
        until(lambda: status() and status()['State'] == '运行中', 'service never started')
        payload()
        until(lambda: status()['Traffic'].get('0', {}).get('Upload', 0) > 0, 'traffic not persisted')
        results.append('CLI create/list and real TCP/UDP forwarding with service metering')
        before = status()['Traffic']['0']['Upload']
        busy = socket.socket()
        busy.bind(('127.0.0.1', 0))
        busy.listen()
        invalid = dict(spec, Listen=f'127.0.0.1:{busy.getsockname()[1]}')
        put(invalid)
        until(lambda: status()['State'] in ('已恢复旧配置', '已保留旧配置'), 'failed edit did not restore old instance')
        payload()
        busy.close()
        results.append('Port-conflicting edit restored the previous working listener')
        put(dict(spec, Revision=1))
        until(lambda: status()['State'] == '运行中', 'valid edit did not apply')
        second = start()
        second.wait(timeout=10)
        assert second.returncode != 0
        payload()
        results.append('Second process rejected by runtime lock; active service remained usable')
        cli('disable', '--id', spec['ID'])
        until(lambda: status()['State'] == '已停用', 'disable did not finish')
        probe = socket.socket()
        probe.bind(('127.0.0.1', service_port))
        probe.close()
        cli('enable', '--id', spec['ID'])
        until(lambda: status()['State'] == '运行中', 'enable did not finish')
        payload()
        results.append('Disable released listener; enable restored forwarding')
        current.send_signal(signal.SIGTERM)
        current.wait(timeout=15)
        assert current.returncode == 0
        current = start()
        until(lambda: status()['State'] == '运行中', 'restart did not restore configuration')
        payload()
        until(lambda: status()['Traffic']['0']['Upload'] > before, 'traffic totals were lost across restart')
        results.append('Actual server restart retained config and accumulated traffic')
        cli('remove', '--id', spec['ID'])
        until(lambda: json.loads(cli('list')) == [], 'service removal did not persist')
        until(lambda: json.loads((root / 'services/.state/status.json').read_text())['runtime-tunnel']['State'] == '已移除', 'running service was not removed')
        assert list((root / 'services').glob('runtime-tunnel.json.bak-*'))
        results.append('Remove closed running service and retained configuration backups')
        current.send_signal(signal.SIGTERM)
        current.wait(timeout=15)
        assert current.returncode == 0
        report = {'passed': len(results), 'checks': results}
        (root / 'report.json').write_text(json.dumps(report, indent=2))
        print(json.dumps(report, indent=2))
    finally:
        for process in processes:
            if process.poll() is None:
                process.kill()
                process.wait()
        for handle in handles:
            handle.close()
        echo.close()
        udp_echo.close()


if __name__ == '__main__':
    main()
