#!/usr/bin/env python3
"""Actual nft apply/rollback in a NEW netns only; never on the host."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile

if os.geteuid() != 0 or os.environ.get('V2BX_SYSTEM_TOOLS_NETNS') != '1':
    raise SystemExit('Explicit root netns opt-in required')
if os.stat('/proc/self/ns/net').st_ino == os.stat('/proc/1/ns/net').st_ino:
    raise SystemExit('Refusing host network namespace')

root = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('system_tools', root/'system-tools.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
subprocess.run(['ip','link','set','lo','up'],check=True)
subprocess.run(['nft','-f','-'],input='''
add table inet v2bx_tools_test
add chain inet v2bx_tools_test early { type filter hook input priority -20; policy drop; }
add chain inet v2bx_tools_test late { type filter hook input priority 20; policy drop; }
add table ip v2bx_hop_keep
add chain ip v2bx_hop_keep prerouting { type nat hook prerouting priority dstnat; }
''',text=True,check=True)

with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as receiver, socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sender:
    receiver.bind(('127.0.0.1',0))
    receiver.settimeout(0.2)
    def received():
        sender.sendto(b'netns-real-packet',receiver.getsockname())
        try:
            return receiver.recv(64) == b'netns-real-packet'
        except TimeoutError:
            return False
    assert not received(), 'baseline must DROP'
    with tempfile.TemporaryDirectory(prefix='v2bx-tools-netns-') as temp:
        state = Path(temp)/'rollback.json'
        state.write_text(json.dumps({'backend':'nft','tag':'v2bx-netns-test'}))
        _, script = m.nft_plan('v2bx-netns-test')
        assert script.count('insert rule') == 2
        subprocess.run(['nft','-f','-'],input=script,text=True,check=True)
        assert received(), 'both early and late chains must ACCEPT'
        m.commit_rules(state)
        m.rollback(state, scheduled=True)
        assert received(), 'committed rules must survive scheduled rollback'
        m.rollback(state)
        assert not received(), 'manual rollback must restore DROP'
        subprocess.run(['nft','list','table','ip','v2bx_hop_keep'],check=True,stdout=subprocess.DEVNULL)
print('PASS: real UDP DROP -> two-priority ACCEPT -> committed -> rollback DROP; NAT preserved')

# Sysctl writes below are confined to this disposable network namespace;
# the persistent drop-in and backups also stay in a temporary directory.
from unittest.mock import patch
with tempfile.TemporaryDirectory(prefix='v2bx-ipv6-netns-') as temp:
    folder = Path(temp)
    backup = folder / 'backup'
    backup.mkdir()
    with patch.object(m, 'backup_dir', return_value=backup):
        m.disable_ipv6(folder / 'disable-ipv6.conf')
    assert all(p.read_text().strip() == '1' for p in Path('/proc/sys/net/ipv6/conf').glob('*/disable_ipv6'))
    assert not subprocess.check_output(['ip', '-6', '-o', 'addr', 'show']).strip()
    assert b'net.ipv6.conf.default.disable_ipv6=1' in (folder / 'disable-ipv6.conf').read_bytes()
print('PASS: IPv6 disabled in a real isolated namespace; persistence file written outside host /etc')
