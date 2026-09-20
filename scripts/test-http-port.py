#!/usr/bin/env python3
"""HTTP port authorization/rollback regressions. Unit tests never signal real processes."""
import contextlib
import errno
import io
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import provision as p


class HTTPPortTests(unittest.TestCase):
    def setUp(self):
        self.owner = {'pid': 123456, 'start': '1024', 'name': 'nginx', 'unit': 'nginx.service'}
        self.props = {'Id': 'nginx.service', 'LoadState': 'loaded', 'ActiveState': 'active',
                      'CanStop': 'yes', 'RefuseManualStop': 'no', 'ControlGroup': '/system.slice/nginx.service',
                      'TriggeredBy': '', 'KillMode': 'control-group'}
        self.checks = [{'http_challenge': True, 'transport': 'udp', 'port': 443}]
        self.enterContext(contextlib.redirect_stdout(io.StringIO()))

    def prepare(self, **kwargs):
        with patch.object(p, 'http_port_free', return_value=False), patch.object(p, 'http_listeners', return_value=[self.owner]), patch.object(p, 'http_service_info', return_value=self.props):
            return p.HTTPPortLease.prepare(self.checks, kwargs.pop('ask', Mock(return_value='Y')), **kwargs)

    def test_no_challenge_never_inspects_or_stops_port(self):
        with patch.object(p, 'http_port_free') as probe, patch.object(p, 'run') as run:
            self.assertIsNone(p.HTTPPortLease.prepare([{}], Mock(), kill=True))
        probe.assert_not_called()
        run.assert_not_called()

    def test_free_port_does_not_need_authorization(self):
        with patch.object(p, 'http_port_free', return_value=True), patch.object(p, 'http_listeners') as owners:
            self.assertIsNone(p.HTTPPortLease.prepare(self.checks, Mock()))
        owners.assert_not_called()

    def test_prepare_yes_only_records_authorization(self):
        for answer in ('Y', 'y'):
            with self.subTest(answer=answer), patch.object(p, 'run') as run:
                lease = self.prepare(ask=Mock(return_value=answer), interactive=True)
                self.assertEqual(lease.stopped, [])
                run.assert_not_called()

    def test_default_cancel_changes_nothing(self):
        for answer in ('N', 'n', '', 'yes'):
            with self.subTest(answer=answer), patch.object(p, 'run') as run:
                with self.assertRaisesRegex(p.PortError, '取消'):
                    self.prepare(ask=Mock(return_value=answer), interactive=True)
                run.assert_not_called()

    def test_yes_deploy_is_not_permission_to_stop_other_services(self):
        with self.assertRaisesRegex(p.PortError, '取消'):
            self.prepare(interactive=False)
        self.assertIsNotNone(self.prepare(release=True))

    def test_check_only_never_acts_even_with_force_flags(self):
        with patch.object(p, 'run') as run:
            with self.assertRaisesRegex(p.PortError, 'check-only'):
                self.prepare(release=True, kill=True, check_only=True)
            run.assert_not_called()

    def test_future_tcp80_listener_cannot_share_restored_service(self):
        self.checks[0].update(transport='tcp', port=80)
        with self.assertRaisesRegex(p.PortError, '共用'):
            self.prepare(release=True)

    def test_raw_requires_separate_exact_confirmation(self):
        self.owner['unit'] = ''
        with patch.object(p.os, 'pidfd_open', create=True), patch.object(p.signal, 'pidfd_send_signal', create=True):
            with self.assertRaisesRegex(p.PortError, '未确认'):
                self.prepare(interactive=True, release=True, ask=Mock(return_value='Y'))
            self.assertTrue(self.prepare(interactive=True, ask=Mock(return_value='KILL-PORT80')).allow_kill)
            self.assertTrue(self.prepare(kill=True).allow_kill)

    def test_existing_v2bx_is_handled_by_deployment_not_killed(self):
        self.owner.update(unit='V2bX.service', name='V2bX')
        lease = self.prepare()
        self.assertFalse(lease.services)
        self.assertFalse(lease.allow_kill)

    def test_release_revalidates_all_owner_identities(self):
        lease = self.prepare(release=True)
        for change in ({'start': 'new'}, {'pid': 999}, {'unit': 'apache2.service'}):
            with self.subTest(change=change), patch.object(p, 'http_port_free', return_value=False), patch.object(p, 'http_listeners', return_value=[dict(self.owner, **change)]), patch.object(p, 'run') as run:
                with self.assertRaisesRegex(p.PortError, '已变化'):
                    lease.release()
                run.assert_not_called()

    def test_release_and_restore_service_without_disable(self):
        lease = self.prepare(release=True)
        with patch.object(p, 'http_port_free', side_effect=[False, True, True, True]), patch.object(p, 'http_listeners', return_value=[self.owner]), patch.object(p, 'http_service_info', return_value=self.props), patch.object(p, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')) as run:
            lease.release()
            lease.restore()
            self.assertEqual(lease.stopped, [])
        self.assertEqual([c.args[:2] for c in run.call_args_list], [('systemctl', 'stop'), ('systemctl', 'start'), ('systemctl', 'is-active')])

    def test_stop_failure_still_attempts_restore(self):
        lease = self.prepare(release=True)
        def commands(*args, **kwargs):
            if args[1] == 'stop':
                raise subprocess.TimeoutExpired(args, 30)
            return subprocess.CompletedProcess(args, 0, '', '')
        with patch.object(p, 'http_port_free', return_value=False), patch.object(p, 'http_listeners', return_value=[self.owner]), patch.object(p, 'http_service_info', return_value=self.props), patch.object(p, 'run', side_effect=commands) as run:
            with self.assertRaises(subprocess.TimeoutExpired):
                lease.release()
            lease.restore()
        self.assertIn(('systemctl', 'start', 'nginx.service'), [c.args for c in run.call_args_list])

    def test_restore_failure_is_not_reported_success(self):
        lease = self.prepare(release=True)
        lease.stopped = ['nginx.service']
        with patch.object(p, 'run', side_effect=p.ProvisionError('failed')):
            with self.assertRaisesRegex(p.PortError, 'systemctl start nginx.service'):
                lease.restore()
        self.assertEqual(lease.stopped, ['nginx.service'])

    def test_raw_term_then_kill_uses_pinned_handles(self):
        self.owner['unit'] = ''
        lease = p.HTTPPortLease([self.owner], {}, True)
        with patch.object(p.signal, 'SIGKILL', 9, create=True), patch.object(p, 'http_port_free', side_effect=[False, False, False, True, True]), patch.object(p, 'http_listeners', return_value=[self.owner]), patch.object(p, 'http_process_info', return_value=self.owner), patch.object(p.os, 'pidfd_open', return_value=17, create=True), patch.object(p.signal, 'pidfd_send_signal', create=True) as send, patch.object(p.os, 'close') as close, patch.object(p.time, 'monotonic', side_effect=[0, 6, 6]):
            lease.release()
        self.assertEqual([c.args for c in send.call_args_list], [(17, signal.SIGTERM), (17, 9)])
        close.assert_called_once_with(17)

    def test_new_listener_after_term_is_not_killed(self):
        self.owner['unit'] = ''
        lease = p.HTTPPortLease([self.owner], {}, True)
        with patch.object(p, 'http_port_free', return_value=False), patch.object(p, 'http_listeners', side_effect=[[self.owner], [dict(self.owner, pid=999)]]), patch.object(p, 'http_process_info', return_value=self.owner), patch.object(p.os, 'pidfd_open', return_value=17, create=True), patch.object(p.signal, 'pidfd_send_signal', create=True) as send, patch.object(p.os, 'close'), patch.object(p.time, 'monotonic', side_effect=[0, 6]):
            with self.assertRaisesRegex(p.PortError, '新的占用者'):
                lease.release()
        send.assert_called_once_with(17, signal.SIGTERM)

    def test_unit_activator_and_protected_units_are_rejected(self):
        for unit in ('sshd.service', 'docker.service', '../nginx.service', 'systemd-networkd.service'):
            with self.subTest(unit=unit), patch.object(p, 'run') as run:
                with self.assertRaises(p.PortError):
                    p.http_service_info(unit)
                run.assert_not_called()
        for change in ({'TriggeredBy': 'nginx.socket'}, {'KillMode': 'process'}, {'RefuseManualStop': 'yes'}, {'ControlGroup': '/other'}):
            props = dict(self.props, **change)
            output = '\n'.join(k + '=' + v for k, v in props.items())
            with self.subTest(change=change), patch.object(p, 'run', return_value=subprocess.CompletedProcess([], 0, output, '')):
                with self.assertRaises(p.PortError):
                    p.http_service_info('nginx.service')

    def test_ipv6_occupancy_and_permission_errors_are_distinguished(self):
        for error, expected in [(errno.EADDRINUSE, False), (errno.EAFNOSUPPORT, True), (errno.EACCES, 'raise')]:
            sock4, sock6 = Mock(), Mock()
            sock4.__enter__ = Mock(return_value=sock4)
            sock4.__exit__ = Mock(return_value=False)
            sock6.__enter__ = Mock(return_value=sock6)
            sock6.__exit__ = Mock(return_value=False)
            sock6.bind.side_effect = OSError(error, 'fixture')
            with self.subTest(error=error), patch.object(p.socket, 'socket', side_effect=[sock4, sock6]):
                if expected == 'raise':
                    with self.assertRaisesRegex(p.PortError, '权限'):
                        p.http_port_free()
                else:
                    self.assertEqual(p.http_port_free(), expected)

    def test_ss_without_pid_is_not_silently_ignored(self):
        with patch.object(p, 'run', return_value=subprocess.CompletedProcess([], 0, 'LISTEN 0 10 0.0.0.0:80 0.0.0.0:*\n', '')):
            with self.assertRaisesRegex(p.PortError, 'PID'):
                p.http_listeners()


def live_netns():
    if (os.geteuid() != 0 or os.environ.get('V2BX_HTTP_PORT_NETNS') != '1'
            or os.readlink('/proc/self/ns/net') == os.readlink('/proc/1/ns/net')):
        raise RuntimeError('Live tests require explicit disposable network namespace; refusing host TCP80')
    if not p.http_port_free():
        raise RuntimeError('Disposable namespace port80 already occupied')
    code = 'import socket,signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); s=socket.socket(); s.bind(("0.0.0.0",80)); s.listen(); print("READY",flush=True); time.sleep(120)'
    child = subprocess.Popen([sys.executable, '-u', '-c', code], stdout=subprocess.PIPE, text=True)
    try:
        assert child.stdout.readline().strip() == 'READY'
        assert not p.http_port_free()
        owners = p.http_listeners()
        assert [o['pid'] for o in owners] == [child.pid]
        # Network namespace may inherit a CI/systemd cgroup. Never stop that service.
        # Exercise only pidfd force termination of this disposable child.
        owners[0]['unit'] = ''
        original = p.http_process_info
        def owned(pid):
            result = original(pid)
            assert pid == child.pid
            result['unit'] = ''
            return result
        with patch.object(p, 'http_process_info', side_effect=owned):
            lease = p.HTTPPortLease(owners, {}, True)
            lease.release()
        child.wait(timeout=5)
        assert child.returncode == -signal.SIGKILL
        assert p.http_port_free()
        print('LIVE_NETNS_TCP80_TERM_KILL_PIDFD_PASS')
    finally:
        if child.poll() is None:
            child.kill()
        child.wait(timeout=5)
        child.stdout.close()


if __name__ == '__main__':
    if sys.argv[1:] == ['--live-netns']:
        live_netns()
    else:
        unittest.main(verbosity=2)
