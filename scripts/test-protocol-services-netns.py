#!/usr/bin/env python3
"""Run protocol/service test binaries in disposable Linux network and mount namespaces.

Does not download backends, modify host routes, or install system services.
Optional external backend paths must be supplied explicitly.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--core-test', type=Path)
    parser.add_argument('--backend-test', type=Path)
    parser.add_argument('--awg', type=Path)
    parser.add_argument('--mtg', type=Path)
    parser.add_argument('--workdir', required=True, type=Path)
    parser.add_argument('--child', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args()
    if sys.platform != 'linux':
        parser.error('requires Linux')
    if not args.core_test and not args.backend_test:
        parser.error('supply --core-test and/or --backend-test')
    if args.backend_test and (not args.awg or not args.mtg):
        parser.error('--backend-test requires --awg and --mtg')
    if not args.child:
        return subprocess.run(['unshare', '--net', '--mount', '--propagation', 'private', '--',
                               sys.executable, str(Path(__file__).resolve()), *sys.argv[1:], '--child']).returncode
    os.umask(0o077)
    subprocess.run(['mount', '-t', 'tmpfs', '-o', 'mode=755', 'tmpfs', '/run'], check=True)
    subprocess.run(['ip', 'link', 'set', 'lo', 'up'], check=True)
    subprocess.run(['ip', 'link', 'add', 'testnet', 'type', 'dummy'], check=True)
    subprocess.run(['ip', 'address', 'add', '192.0.2.10/24', 'dev', 'testnet'], check=True)
    subprocess.run(['ip', 'link', 'set', 'testnet', 'up'], check=True)
    subprocess.run(['ip', 'route', 'add', 'default', 'dev', 'testnet'], check=True)
    work = args.workdir.resolve()
    work.mkdir(parents=True, exist_ok=True)
    temporary = work / 'tmp'
    temporary.mkdir(exist_ok=True)
    # Keep protocol scratch data on a private Linux filesystem even when
    # --workdir is a Windows/WSL shared mount. Logs remain in --workdir.
    subprocess.run(['mount', '-t', 'tmpfs', '-o', 'mode=700', 'tmpfs', str(temporary)], check=True)
    env = os.environ.copy()
    env.update(V2BX_ISOLATED_TEST='1', GOMAXPROCS='4', TMPDIR=str(temporary))
    report = []
    jobs = []
    if args.core_test:
        jobs.append(('core', args.core_test.resolve(), 'TestExtended|TestExported|TestLocal'))
    if args.backend_test:
        env.update(V2BX_AWG_BINARY=str(args.awg.resolve()), V2BX_MTG_BINARY=str(args.mtg.resolve()))
        jobs.append(('backends', args.backend_test.resolve(), 'TestReal'))
    success = True
    for name, binary, pattern in jobs:
        result = subprocess.run([str(binary), '-test.run=' + pattern, '-test.v', '-test.timeout=180s'],
                                env=env, capture_output=True, timeout=200)
        (work / (name + '-stdout.log')).write_bytes(result.stdout)
        (work / (name + '-stderr.log')).write_bytes(result.stderr)
        text = result.stdout.decode(errors='replace').splitlines()
        report.append({'suite': name, 'exit': result.returncode,
                       'results': [line for line in text if '--- PASS:' in line or '--- FAIL:' in line
                                   or '--- SKIP:' in line or '.go:' in line]})
        success = success and result.returncode == 0
    (work / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if success else 1


if __name__ == '__main__':
    raise SystemExit(main())
