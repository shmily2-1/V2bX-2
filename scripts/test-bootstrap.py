#!/usr/bin/env python3
"""Offline tests for the original-style V2bX-2 bootstrap and config wizard."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[1]
BOOTSTRAP = ROOT / 'install.sh'
HELPER = ROOT / 'scripts/install.sh'
MANAGEMENT = ROOT / 'V2bX.sh'
INITCONFIG = ROOT / 'initconfig.sh'
CONFIGURE = ROOT / 'configure.py'
BINARY = b'#!/usr/bin/env bash\n[[ ${1:-} == version ]] || exit 99\necho "fixture V2bX"\n'
SAMPLE = b'{"NodeType":"hysteria","version":2}\n'
RUNTIME_ASSETS = ('geoip.dat', 'geosite.dat', 'geoip.db', 'geosite.db',
                  'dns.json', 'route.json', 'custom_inbound.json', 'custom_outbound.json')


class BootstrapTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='v2bx-bootstrap-test-')
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.root = self.base / 'root'
        self.mock = self.base / 'mock'
        self.assets = self.base / 'assets'
        self.mock.mkdir()
        self.assets.mkdir()
        self.log = self.base / 'fetch.log'
        self.write_mock('curl', '''
import json, os, pathlib, shutil, sys
args = sys.argv[1:]
url = next(value for value in args if value.startswith('https://'))
assert url.startswith(('https://raw.githubusercontent.com/shmily2-1/V2bX-2/', 'https://github.com/shmily2-1/V2bX-2/releases/'))
out = args[args.index('-o') + 1]
path = pathlib.Path(os.environ['FIXTURE_ASSETS'])
if url.endswith('/scripts/install.sh'):
    source = pathlib.Path(os.environ['FIXTURE_HELPER'])
else:
    source = path / url.rsplit('/', 1)[1]
with open(os.environ['FIXTURE_LOG'], 'a') as handle:
    handle.write(json.dumps(url) + '\\n')
shutil.copyfile(source, out)
''')
        self.write_mock('unzip', '''
import pathlib, sys, zipfile
args = sys.argv[1:]
archive = pathlib.Path(args[args.index('-p') + 1])
member = args[args.index('-p') + 2]
sys.stdout.buffer.write(zipfile.ZipFile(archive).read(member))
''')
        self.env = os.environ | {
            'PATH': str(self.mock) + os.pathsep + os.environ['PATH'],
            'FIXTURE_ASSETS': str(self.assets),
            'FIXTURE_LOG': str(self.log),
            'FIXTURE_HELPER': str(HELPER),
        }
        archive = self.assets / 'V2bX-linux-amd64.zip'
        with zipfile.ZipFile(archive, 'w') as bundle:
            bundle.writestr('V2bX', BINARY)
            bundle.writestr('example/hysteria2-xboard.json', SAMPLE)
            for name in RUNTIME_ASSETS:
                bundle.writestr(f'example/{name}', f'fixture-{name}\n'.encode())
            bundle.writestr('management/V2bX.sh', MANAGEMENT.read_bytes())
            bundle.writestr('management/initconfig.sh', INITCONFIG.read_bytes())
            bundle.writestr('management/configure.py', CONFIGURE.read_bytes())
            for name in ('provision.py', 'system-tools.py'):
                bundle.writestr('management/' + name, (ROOT / name).read_bytes())
            bundle.writestr('management/install.sh', HELPER.read_bytes())
            bundle.writestr('management/bootstrap.sh', BOOTSTRAP.read_bytes())
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (self.assets / 'V2bX-linux-amd64.zip.sha256').write_text(
            f'{digest}  V2bX-linux-amd64.zip\n', encoding='utf-8')

    def write_mock(self, name, body):
        path = self.mock / name
        path.write_text('#!/usr/bin/env python3\n' + body, encoding='utf-8')
        path.chmod(0o755)

    def run_bootstrap(self, *extra, ok=True):
        result = subprocess.run(
            ['bash', str(BOOTSTRAP), '--destdir', str(self.root), '--non-interactive', *extra],
            env=self.env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
        self.assertEqual(result.returncode == 0, ok, result.stdout)
        return result.stdout

    def test_original_style_bootstrap_stages_binary_manager_unit_and_template(self):
        output = self.run_bootstrap()
        self.assertIn('Staging only:', output)
        self.assertEqual((self.root / 'usr/local/V2bX/V2bX').read_bytes(), BINARY)
        self.assertTrue((self.root / 'usr/local/lib/V2bX-2/bootstrap.sh').exists())
        self.assertTrue((self.root / 'usr/local/lib/V2bX-2/V2bX.sh').exists())
        self.assertTrue((self.root / 'usr/local/lib/V2bX-2/configure.py').exists())
        self.assertTrue((self.root / 'usr/bin/V2bX').is_symlink())
        self.assertTrue((self.root / 'usr/local/bin/v2bx').is_symlink())
        self.assertTrue((self.root / 'etc/systemd/system/V2bX.service').exists())
        self.assertEqual((self.root / 'etc/V2bX/config.json.example').read_bytes(), SAMPLE)
        self.assertEqual((self.root / 'etc/V2bX/geoip.dat').read_text(), 'fixture-geoip.dat\n')
        self.run_menu('version')

    def test_menu_version_dispatch_uses_manager_state_not_display_state(self):
        self.run_bootstrap()
        output = self.run_menu('menu', text='12\n17\n')
        self.assertIn('fixture V2bX', output)
        self.assertNotIn('操作失败', output)
        for label in ('8. 查看 V2bX 日志', '9. 设置 V2bX 开机自启', '10. 取消 V2bX 开机自启', '16. 放行 VPS'):
            self.assertIn(label, output)

    def test_isolated_menu_uses_recorded_binary(self):
        self.run_bootstrap()
        result = subprocess.run(
            [str(self.root / 'usr/bin/V2bX'), '--root', str(self.root), 'help'],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn('不关闭防火墙', result.stdout)

    def test_update_preserves_config_and_service(self):
        self.run_bootstrap()
        config = self.root / 'etc/V2bX/config.json'
        unit = self.root / 'etc/systemd/system/V2bX.service'
        config.write_text('{"secret":"keep"}\n', encoding='utf-8')
        unit.write_text(unit.read_text(encoding='utf-8') + '\n# keep\n', encoding='utf-8')
        before_config = config.read_bytes()
        before_unit = unit.read_bytes()
        self.run_bootstrap()
        self.assertEqual(config.read_bytes(), before_config)
        self.assertEqual(unit.read_bytes(), before_unit)
        backups = list((self.root / 'usr/local/V2bX').glob('V2bX.backup.*'))
        self.assertEqual(len(backups), 1)

    def test_checksum_failure_does_not_stage_binary(self):
        (self.assets / 'V2bX-linux-amd64.zip.sha256').write_text('0' * 64 + '  V2bX-linux-amd64.zip\n', encoding='utf-8')
        self.run_bootstrap(ok=False)
        self.assertFalse((self.root / 'usr/local/V2bX/V2bX').exists())

    def test_config_wizard_encodes_user_values_and_new_field_names(self):
        config_dir = self.base / 'config'
        answers = self.node_answers(key='key"with\\nquote') + ['n', 'SAVE']
        output = self.run_wizard(answers)
        document = json.loads((config_dir / 'config.json').read_text(encoding='utf-8'))
        node = document['Nodes'][0]
        self.assertEqual(node['ApiKey'], 'key"with\\nquote')
        self.assertEqual(node['ReportMinTraffic'], 0)
        self.assertNotIn('MinReportTraffic', node)
        self.assertEqual(node['EnableSniff'], True)
        self.assertEqual(node['CertConfig']['CertMode'], 'none')
        self.assertNotIn('key"with', output)
        self.assertEqual((config_dir / 'config.json').stat().st_mode & 0o777, 0o600)

    def node_answers(self, core='sing', protocol='vless', key='local-test-key', node_id='7'):
        answers = [core, node_id, 'https://test-panel.invalid', key]
        if core != 'hysteria2':
            answers += [protocol]
        if protocol == 'hysteria2' or core == 'hysteria2':
            answers += ['xboard']
        answers += ['', '']  # IPv4 defaults
        if protocol in ('hysteria', 'hysteria2', 'trojan', 'tuic', 'anytls') or core == 'hysteria2':
            answers += ['file', 'node.test.invalid', '', '']
        else:
            answers += ['none']
        return answers

    def run_wizard(self, answers, ok=True, *extra):
        result = subprocess.run(
            ['bash', str(INITCONFIG), '--root', str(self.base), '--config', str(self.base / 'config/config.json'), *extra],
            input='\n'.join(answers) + '\n', text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=15)
        self.assertEqual(result.returncode == 0, ok, result.stdout)
        return result.stdout

    def run_menu(self, *args, ok=True, text=''):
        result = subprocess.run(
            [str(self.root / 'usr/bin/V2bX'), '--root', str(self.root), *args],
            env=self.env, input=text, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
        self.assertEqual(result.returncode == 0, ok, result.stdout)
        return result.stdout

    def test_multinode_hysteria_and_field_isolation(self):
        self.run_wizard(self.node_answers(core='xray') + ['y'] + self.node_answers(core='hysteria2', node_id='8') + ['n', 'SAVE'])
        document = json.loads((self.base / 'config/config.json').read_text())
        self.assertEqual([core['Type'] for core in document['Cores']], ['xray', 'hysteria2'])
        self.assertEqual(document['Nodes'][1]['NodeType'], 'hysteria')
        self.assertEqual(document['Nodes'][1]['CertConfig']['CertMode'], 'file')
        self.assertNotIn('Hysteria2ConfigPath', document['Nodes'][1])
        self.assertNotIn('EnableSniff', document['Nodes'][1])
        self.assertEqual(list((self.base / 'config').glob('*.yaml')), [])

    def test_sing_hysteria2_uses_xboard_api(self):
        self.run_wizard(self.node_answers(protocol='hysteria2') + ['n', 'SAVE'])
        node = json.loads((self.base / 'config/config.json').read_text())['Nodes'][0]
        self.assertEqual(node['Core'], 'sing')
        self.assertEqual(node['NodeType'], 'hysteria')

    def test_cancel_overwrite_leaves_bytes_unchanged(self):
        self.run_wizard(self.node_answers() + ['n', 'SAVE'])
        config = self.base / 'config/config.json'
        original = config.read_bytes()
        self.run_wizard(['n'])
        self.assertEqual(original, config.read_bytes())
        self.assertEqual(list(config.parent.glob('*.backup.*')), [])

    def test_overwrite_backup_is_private_and_unique(self):
        self.run_wizard(self.node_answers() + ['n', 'SAVE'])
        config = self.base / 'config/config.json'
        before = config.read_bytes()
        self.run_wizard(['REPLACE'] + self.node_answers(node_id='8') + ['n', 'SAVE'])
        self.run_wizard(['REPLACE'] + self.node_answers(node_id='9') + ['n', 'SAVE'])
        backups = list(config.parent.glob('*.backup.*'))
        self.assertEqual(len(backups), 2)
        self.assertIn(before, [p.read_bytes() for p in backups])
        self.assertTrue(all(p.stat().st_mode & 0o777 == 0o600 for p in backups))

    def test_eof_before_save_preserves_config(self):
        self.run_wizard(self.node_answers() + ['n', 'SAVE'])
        config = self.base / 'config/config.json'
        before = config.read_bytes()
        self.run_wizard(['REPLACE', 'sing'], ok=False)
        self.assertEqual(before, config.read_bytes())

    def test_staging_forbids_host_service_calls(self):
        self.run_bootstrap()
        for command in ('start', 'stop', 'restart', 'enable', 'disable', 'log', 'bbr', 'open-ports'):
            with self.subTest(command=command):
                self.assertIn('隔离模式禁止', self.run_menu(command, ok=False))

    def test_menu_update_uses_new_repo(self):
        self.run_bootstrap()
        output = self.run_menu('update', 'v0.1.0-core-upgrade.3')
        self.assertIn('旧进程未重启', output)
        urls = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertTrue(all('/shmily2-1/V2bX-2/' in url for url in urls))
        self.assertTrue((self.root / 'usr/local/lib/V2bX-2/install.sh').exists())

    def test_uninstall_cancel(self):
        self.run_bootstrap()
        self.run_menu('uninstall', text='n\n')
        self.assertTrue((self.root / 'usr/local/V2bX/V2bX').exists())

    def test_uninstall_preserves_configuration(self):
        self.run_bootstrap()
        config = self.root / 'etc/V2bX/config.json'
        config.write_text('private-existing-config')
        self.run_menu('uninstall', text='UNINSTALL\n')
        self.assertEqual(config.read_text(), 'private-existing-config')
        self.assertFalse((self.root / 'usr/local/V2bX/V2bX').exists())

    def test_old_install_script_and_runtime_files_preserved(self):
        path = self.root / 'usr/local/V2bX/V2bX'
        path.parent.mkdir(parents=True)
        path.write_text('old-core')
        manager = self.root / 'usr/bin/V2bX'
        manager.parent.mkdir(parents=True)
        manager.write_text('#!/bin/bash\n# V2bX original menu\necho old\n')
        (manager.parent / 'v2bx').symlink_to('V2bX')
        route = self.root / 'etc/V2bX/route.json'
        route.parent.mkdir(parents=True)
        route.write_text('custom-route-keep')
        self.run_bootstrap()
        self.assertEqual(route.read_text(), 'custom-route-keep')
        self.assertTrue(list(manager.parent.glob('V2bX.backup.*')))
        self.run_menu('version')

    def test_previous_modern_binary_is_not_overwritten_by_menu(self):
        binary = self.root / 'usr/local/bin/V2bX'
        binary.parent.mkdir(parents=True)
        binary.write_text('old-modern-core')
        self.run_bootstrap()
        self.assertEqual(binary.read_bytes(), BINARY)
        self.assertFalse(binary.is_symlink())
        self.assertTrue((binary.parent / 'v2bx').is_symlink())
        self.run_menu('version')

    def test_reject_unknown_usr_bin_command(self):
        binary = self.root / 'usr/bin/V2bX'
        binary.parent.mkdir(parents=True)
        binary.write_bytes(b'\x7fELF do-not-replace')
        self.run_bootstrap(ok=False)
        self.assertEqual(binary.read_bytes(), b'\x7fELF do-not-replace')

    def test_manager_state_symlink_escape_fails(self):
        outside = self.base / 'outside'
        outside.mkdir()
        parent = self.root / 'usr/local/lib'
        parent.mkdir(parents=True)
        (parent / 'V2bX-2').symlink_to(outside, target_is_directory=True)
        self.run_bootstrap(ok=False)
        self.assertEqual(list(outside.iterdir()), [])

    def test_management_help_has_no_old_remote_installer(self):
        result = subprocess.run(['bash', str(MANAGEMENT), 'help'], text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10)
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn('不执行原版安装器', result.stdout)
        self.assertNotIn('wyx2685/V2bX-script/master/install.sh', result.stdout)


if __name__ == '__main__':
    unittest.main(verbosity=2)
