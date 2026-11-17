#!/usr/bin/env python3
"""IPv6 persistence and rollback with a temporary sysctl tree; no host changes."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('system_tools', ROOT / 'system-tools.py')
tools = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tools)


class IPv6Tests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.destination = self.root / 'disable-ipv6.conf'
        self.parameters = self.root / 'ipv6'
        for name in ('all', 'default', 'lo', 'eth0'):
            path = self.parameters / name / 'disable_ipv6'
            path.parent.mkdir(parents=True)
            path.write_text('0\n')
        self.backup = self.root / 'backup'
        self.backup.mkdir()

    def apply(self, *args):
        self.assertEqual(args, ('sysctl', '-p', str(self.destination)))
        for path in self.parameters.glob('*/disable_ipv6'):
            path.write_text('1\n')
        return subprocess.CompletedProcess(args, 0, '', '')

    def test_disables_existing_and_future_interfaces_and_saves_original(self):
        self.destination.write_text('# previous configuration\n')
        with patch.object(tools, 'backup_dir', return_value=self.backup), patch.object(tools, 'run', side_effect=self.apply):
            tools.disable_ipv6(self.destination, self.parameters)
        self.assertIn('net.ipv6.conf.default.disable_ipv6=1', self.destination.read_text())
        self.assertIn('net.ipv6.conf.all.disable_ipv6=1', self.destination.read_text())
        self.assertTrue(all(p.read_text().strip() == '1' for p in self.parameters.glob('*/disable_ipv6')))
        self.assertEqual((self.backup / self.destination.name).read_text(), '# previous configuration\n')
        self.assertEqual(set(json.loads((self.backup / 'ipv6-state.json').read_text())['parameters'].values()), {'0'})

    def test_failed_apply_restores_configuration_and_runtime_values(self):
        self.destination.write_text('# retain this file\n')
        def fail(*args):
            self.apply(*args)
            raise RuntimeError('sysctl failed')
        with patch.object(tools, 'backup_dir', return_value=self.backup), patch.object(tools, 'run', side_effect=fail):
            with self.assertRaisesRegex(RuntimeError, 'sysctl failed'):
                tools.disable_ipv6(self.destination, self.parameters)
        self.assertEqual(self.destination.read_text(), '# retain this file\n')
        self.assertTrue(all(p.read_text().strip() == '0' for p in self.parameters.glob('*/disable_ipv6')))

    def test_verification_failure_does_not_leave_a_new_config(self):
        with patch.object(tools, 'backup_dir', return_value=self.backup), patch.object(tools, 'run'):
            with self.assertRaisesRegex(RuntimeError, '核验失败'):
                tools.disable_ipv6(self.destination, self.parameters)
        self.assertFalse(self.destination.exists())

    def test_module_absent_still_saves_persistent_configuration(self):
        with patch.object(tools, 'backup_dir', return_value=self.backup), patch.object(tools, 'run') as run:
            tools.disable_ipv6(self.destination, self.root / 'absent')
        run.assert_not_called()
        self.assertIn('net.ipv6.conf.all.disable_ipv6=1', self.destination.read_text())


if __name__ == '__main__':
    unittest.main()
