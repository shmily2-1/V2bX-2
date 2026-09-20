#!/usr/bin/env python3
"""Loopback panel + real file transactions. systemd/CA/firewalls are simulated, never host-modified."""
import contextlib
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import socket
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
import provision as p
spec = importlib.util.spec_from_file_location('system_tools', ROOT / 'system-tools.py')
system_tools = importlib.util.module_from_spec(spec)
spec.loader.exec_module(system_tools)


class ProvisionTests(unittest.TestCase):
    def setUp(self):
        # Unit wizard answers must not escape input mocks into a real TTY/getpass.
        self.enterContext(patch.object(p.cfg.sys.stdin, 'isatty', return_value=False))
        self.temp = tempfile.TemporaryDirectory(prefix='v2bx-provision-test-')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.info = {'protocol': 'vless', 'server_port': 443, 'tls': 0}
        self.users, self.status, self.requests = [{'id': 1, 'uuid': 'only-loopback-test'}], 200, []
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                from urllib.parse import urlsplit, parse_qs
                parsed = urlsplit(self.path)
                owner.requests.append(parse_qs(parsed.query))
                self.send_response(owner.status)
                if owner.status == 302:
                    self.send_header('Location', '/must-not-follow')
                self.end_headers()
                self.wfile.write(json.dumps(owner.info if parsed.path.endswith('/config') else {'users': owner.users}).encode())

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.node = {'Core': 'sing', 'ApiHost': 'http://127.0.0.1:' + str(self.server.server_port),
                     'ApiKey': 'test-private&secret', 'NodeID': 1, 'NodeType': 'vless', 'CertConfig': {'CertMode': 'none'}}
        self.document = {'Cores': [{'Type': 'sing'}], 'Nodes': [self.node]}

    def test_edit_http_requires_explicit_confirmation(self):
        with patch.object(p.cfg, 'ask', return_value='INSECURE-HTTP') as ask:
            self.assertTrue(p.confirm_http_panel(self.document, interactive=True))
            ask.assert_called_once()
        with patch.object(p.cfg, 'ask') as ask:
            self.assertFalse(p.confirm_http_panel(self.document))
            self.assertTrue(p.confirm_http_panel(self.document, allowed=True))
            ask.assert_not_called()
        with patch.object(p.cfg, 'ask', return_value='cancel'):
            self.assertFalse(p.confirm_http_panel(self.document, interactive=True))


    def test_online_wizard_y_continues_n_finishes_one_node_at_a_time(self):
        answers = []
        for core, node_id, more in [('sing', '21', 'Y'), ('xray', '22', 'N')]:
            answers += [core, self.node['ApiHost'], 'INSECURE-HTTP', self.node['ApiKey'],
                        node_id, 'vless', more]
        with patch('builtins.input', side_effect=answers), contextlib.redirect_stdout(io.StringIO()) as output:
            document, insecure = p.wizard()
        self.assertEqual([node['NodeID'] for node in document['Nodes']], [21, 22])
        self.assertEqual([core['Type'] for core in document['Cores']], ['sing', 'xray'])
        self.assertEqual([query['node_id'] for query in self.requests], [['21'], ['22']])
        self.assertTrue(insecure)
        self.assertNotIn(self.node['ApiKey'], output.getvalue())

    def test_online_wizard_n_lowercase_or_default_stops_after_one(self):
        for answer in ('N', 'n', ''):
            with self.subTest(answer=answer):
                answers = ['sing', self.node['ApiHost'], 'INSECURE-HTTP', self.node['ApiKey'],
                           '1', 'vless', answer]
                with patch('builtins.input', side_effect=answers), contextlib.redirect_stdout(io.StringIO()):
                    document, _ = p.wizard()
                self.assertEqual(len(document['Nodes']), 1)

    def test_yes_no_prompt_accepts_both_cases_and_reprompts_invalid_choice(self):
        for answer, expected in [('Y', 'y'), ('y', 'y'), ('N', 'n'), ('n', 'n'), ('', 'n')]:
            with self.subTest(answer=answer), patch('builtins.input', return_value=answer) as ask:
                self.assertEqual(p.cfg.choose('是否继续添加节点？', ('y', 'n'), 'n'), expected)
                self.assertIn('(Y/N) [N]', ask.call_args.args[0])
        with patch('builtins.input', side_effect=['invalid', 'Y']), contextlib.redirect_stdout(io.StringIO()) as output:
            self.assertEqual(p.cfg.choose('是否继续添加节点？', ('y', 'n'), 'n'), 'y')
        self.assertIn('无效选项', output.getvalue())

    def test_real_panel_query_and_users(self):
        checks = p.preflight(self.document, allow_insecure_panel=True)
        self.assertEqual(checks[0]['port'], 443)
        self.assertEqual(checks[0]['users'], 1)
        self.assertEqual(self.requests[0]['token'], ['test-private&secret'])

    def test_redirect_rejected_without_leaking_token(self):
        self.status = 302
        with self.assertRaises(p.ProvisionError) as error:
            p.inspect_node(self.node)
        self.assertEqual(len(self.requests), 1)
        self.assertNotIn(self.node['ApiKey'], str(error.exception))

    def test_auth_error_is_redacted(self):
        self.status = 403
        with self.assertRaises(p.ProvisionError) as error:
            p.inspect_node(self.node)
        self.assertIn('403', str(error.exception))
        self.assertNotIn('secret', str(error.exception))

    def test_empty_users_explicit_opt_in(self):
        self.users = []
        with self.assertRaises(p.ProvisionError):
            p.preflight(self.document, allow_insecure_panel=True)
        self.node['NodeType'] = 'hysteria'
        self.info.update(protocol='hysteria', version=2)
        self.node['CertConfig'] = {'CertMode':'self', 'CertDomain':'test.invalid', 'CertFile':str(self.base/'cert'), 'KeyFile':str(self.base/'key')}
        self.assertEqual(p.preflight(self.document, allow_empty=True, allow_insecure_panel=True)[0]['users'], 0)

    def test_tls_required_and_reality_does_not_request_cert(self):
        self.info['tls'] = 1
        with self.assertRaises(p.ProvisionError):
            p.preflight(self.document)
        self.info['tls'] = 2
        self.assertEqual(len(p.preflight(self.document, allow_insecure_panel=True)), 1)

    def test_hysteria2_detected_from_version(self):
        self.node['NodeType'] = 'hysteria'
        self.info = {'protocol': 'hysteria', 'version': 2, 'server_port': 443, 'ports': '10000-50000'}
        check = p.inspect_node(self.node)
        self.assertEqual((check['protocol'], check['transport']), ('hysteria2', 'udp'))

    def test_missing_certificate_stops_before_service(self):
        self.info['tls'] = 1
        self.node['CertConfig'] = {'CertMode': 'file', 'CertDomain': 'node.invalid',
                                   'CertFile': str(self.base/'cert.pem'), 'KeyFile': str(self.base/'key.pem')}
        with self.assertRaises(p.ProvisionError):
            p.preflight(self.document)

    def test_wrong_dns_stops_without_ca(self):
        self.info['tls'] = 1
        self.node['CertConfig'] = {'CertMode': 'http', 'CertDomain': 'node.invalid',
                                   'CertFile': str(self.base/'cert.pem'), 'KeyFile': str(self.base/'key.pem')}
        original = socket.getaddrinfo
        def valid_loopback(host, *args, **kwargs):
            if host == 'node.invalid':
                return [(socket.AF_INET, socket.SOCK_STREAM, 6, '', ('127.0.0.1', 80))]
            return original(host, *args, **kwargs)
        with patch.object(p.socket, 'getaddrinfo', side_effect=valid_loopback):
            with self.assertRaisesRegex(p.ProvisionError, '公网IP'):
                p.preflight(self.document, public_ip='192.0.2.1', allow_insecure_panel=True)

    def test_reuse_valid_same_node_certificate_preserves_acme(self):
        cert = {'CertMode': 'http', 'CertDomain': 'node.invalid', 'Email': 'existing@example.test',
                'CertFile': str(self.base/'cert.pem'), 'KeyFile': str(self.base/'key.pem')}
        for key in ('CertFile', 'KeyFile'):
            Path(cert[key]).write_text('fixture; openssl is isolated')
        self.node['CertConfig'] = cert
        with patch.object(p, 'certificate_valid'):
            result = p.reusable_certificate(self.document, self.node, 'node.invalid', 'http', [])
            self.assertEqual(result, cert)
            self.assertIsNot(result, cert)
            self.assertIsNone(p.reusable_certificate(self.document, self.node, 'other.invalid', 'http', []))
            self.assertIsNone(p.reusable_certificate(self.document, self.node, 'node.invalid', 'dns', []))
            self.assertIsNone(p.reusable_certificate(self.document, dict(self.node, NodeID=2), 'node.invalid', 'http', []))
            self.assertIsNone(p.reusable_certificate(self.document, dict(self.node, ApiHost='https://other.invalid'), 'node.invalid', 'http', []))
            self.assertIsNone(p.reusable_certificate(self.document, self.node, 'node.invalid', 'http', [self.node]))

    def test_invalid_existing_certificate_is_not_reused(self):
        self.node['CertConfig'] = {'CertMode': 'http', 'CertDomain': 'node.invalid',
                                  'CertFile': str(self.base/'cert'), 'KeyFile': str(self.base/'key')}
        for key in ('CertFile', 'KeyFile'):
            Path(self.node['CertConfig'][key]).touch()
        with patch.object(p, 'certificate_valid', side_effect=p.ProvisionError('invalid')):
            self.assertIsNone(p.reusable_certificate(self.document, self.node, 'node.invalid', 'http', []))

    def test_wizard_reuses_certificate_without_asking_for_acme_email(self):
        self.info.update(tls=1, server_name='node.invalid')
        cert = {'CertMode': 'http', 'CertDomain': 'node.invalid', 'Email': 'retained@example.test',
                'CertFile': str(self.base/'cert'), 'KeyFile': str(self.base/'key')}
        answers = ['sing', self.node['ApiHost'], 'INSECURE-HTTP', self.node['ApiKey'], '1', 'vless', '', '', 'N']
        with patch.object(p, 'reusable_certificate', return_value=copy.deepcopy(cert)), patch('builtins.input', side_effect=answers):
            document, _ = p.wizard(self.document)
        self.assertEqual(document['Nodes'][0]['CertConfig'], cert)

    def test_valid_existing_http_certificate_does_not_require_free_port(self):
        self.info['tls'] = 1
        self.node['CertConfig'] = {'CertMode': 'http', 'CertDomain': 'node.invalid',
                                  'CertFile': str(self.base/'cert'), 'KeyFile': str(self.base/'key')}
        for key in ('CertFile', 'KeyFile'):
            Path(self.node['CertConfig'][key]).touch()
        with patch.object(p, 'certificate_valid'), patch.object(p, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')), patch.object(p, 'http_port_free') as probe:
            checks = p.preflight(self.document, allow_insecure_panel=True)
        self.assertFalse(checks[0].get('http_challenge'))
        probe.assert_not_called()

    def test_busy_http_port_only_deferred_with_explicit_preflight_mode(self):
        self.info['tls'] = 1
        self.node['CertConfig'] = {'CertMode': 'http', 'CertDomain': 'node.invalid',
                                  'CertFile': str(self.base/'cert'), 'KeyFile': str(self.base/'key')}
        resolved = [(socket.AF_INET, socket.SOCK_STREAM, 6, '', ('192.0.2.1', 80))]
        with patch.object(p, 'inspect_node', return_value={'info': self.info, 'protocol': 'vless', 'transport': 'tcp', 'port': 443, 'users': 1}), patch.object(p.socket, 'getaddrinfo', return_value=resolved), patch.object(p, 'http_port_free', return_value=False):
            with self.assertRaisesRegex(p.ProvisionError, '临时释放'):
                p.preflight(self.document, allow_insecure_panel=True)
            checks = p.preflight(self.document, allow_insecure_panel=True, allow_busy_http=True)
        self.assertTrue(checks[0]['http_challenge'])

    def deployment(self, failure=False, existing=True, interruption=False, lease=None):
        path = self.base/'config.json'
        old = b'{"Cores": [{"Type": "sing"}], "Nodes": [], "preserve":"original bytes"}\n' if existing else None
        if existing:
            path.write_bytes(old)
        calls = []

        def fake_run(*args, **kwargs):
            calls.append(args)
            output = ''
            code = 0
            if args[1:2] == ('is-active',):
                code = 0 if existing else 3
            if args[1:2] == ('is-enabled',):
                code = 1
            if args[1:2] == ('show',):
                output = '{ path=/usr/local/bin/V2bX ; argv[]=V2bX server --config ' + str(path) + ' ; }'
            return subprocess.CompletedProcess(args, code, output, '')

        def ready(*_):
            self.assertEqual(json.loads(path.read_bytes()), self.document)
            if os.name != 'nt':
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            if interruption:
                raise KeyboardInterrupt()
            if failure:
                raise p.ProvisionError('simulated CA failure; no real CA contacted')
            return '12345'

        dropin = self.base/'dropin.conf'
        with patch.object(p, 'DROPIN', dropin), patch.object(p, 'run', side_effect=fake_run), patch.object(p, 'wait_healthy', side_effect=ready):
            if failure or interruption:
                with self.assertRaises((p.ProvisionError, KeyboardInterrupt)):
                    p.deploy(path, self.document, [{'users':1}], old, 30, True, lease)
            else:
                p.deploy(path, self.document, [{'users':1}], old, 30, True, lease)
        self.assertFalse(dropin.exists())
        if failure or interruption:
            if existing:
                self.assertEqual(json.loads(path.read_bytes()), json.loads(old))
            else:
                self.assertFalse(path.exists())
        else:
            self.assertEqual(json.loads(path.read_bytes()), self.document)
            self.assertIn(('systemctl', 'enable', p.SERVICE), calls)
        self.assertLess(calls.index(('systemctl','stop',p.SERVICE)), calls.index(('systemctl','start',p.SERVICE)))

    def test_deploy_success_real_atomic_files(self):
        self.deployment()

    def test_deploy_failure_restores_existing(self):
        self.deployment(failure=True)

    def test_first_deploy_failure_removes_config(self):
        self.deployment(failure=True, existing=False)

    def test_interruption_rolls_back(self):
        self.deployment(interruption=True)

    def test_port_lease_restored_after_success_failure_and_interruption(self):
        from unittest.mock import Mock
        for options in ({}, {'failure': True}, {'interruption': True}):
            with self.subTest(options=options):
                lease = Mock()
                self.deployment(lease=lease, **options)
                lease.release.assert_called_once()
                lease.restore.assert_called()


class MaintenanceTests(unittest.TestCase):
    @unittest.skipIf(system_tools.fcntl is None, 'requires Linux flock')
    def test_committed_rule_is_not_removed_by_scheduled_rollback(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/'state.json'
            path.write_text(json.dumps({'tag':'test', 'backend':'nft'}))
            system_tools.commit_rules(path)
            with patch.object(system_tools, 'run') as run:
                system_tools.rollback(path, scheduled=True)
                run.assert_not_called()
            self.assertTrue(path.exists())
            response = subprocess.CompletedProcess([],0,'{"nftables":[]}', '')
            with patch.object(system_tools, 'run', return_value=response):
                system_tools.rollback(path)
            self.assertFalse(path.exists())

    def test_bbr_cancel_does_not_install_or_change_sysctl(self):
        result = subprocess.CompletedProcess([],0,'bbr\n','')
        with patch.object(system_tools,'run',return_value=result) as run, patch('builtins.input',return_value='cancel'):
            system_tools.bbr()
            self.assertEqual(run.call_count,1)
            self.assertEqual(run.call_args.args,('sysctl','-n','net.ipv4.tcp_congestion_control'))

    def test_open_ports_cancel_runs_nothing(self):
        with patch.object(system_tools,'run') as run, patch('builtins.input',return_value='cancel'):
            system_tools.open_ports()
            run.assert_not_called()

    def test_nft_all_input_chains_and_no_nat_flush(self):
        data = {'nftables':[{'chain':{'family':'inet','table':'a','name':'input','hook':'input','type':'filter'}},
                             {'chain':{'family':'ip','table':'b','name':'in','hook':'input','type':'filter'}},
                             {'chain':{'family':'ip','table':'v2bx_hop_test','name':'nat','hook':'prerouting','type':'nat'}}]}
        with patch.object(system_tools,'run',return_value=subprocess.CompletedProcess([],0,json.dumps(data),'')):
            _, script = system_tools.nft_plan('test-rule')
        self.assertEqual(script.count('insert rule'),2)
        self.assertNotIn('v2bx_hop',script)
        self.assertNotIn('flush',script)

    def test_nft_preinput_filters_refused(self):
        data = {'nftables':[{'chain':{'family':'inet','table':'a','name':'raw','hook':'prerouting','type':'filter'}}]}
        with patch.object(system_tools,'run',return_value=subprocess.CompletedProcess([],0,json.dumps(data),'')):
            with self.assertRaises(RuntimeError):
                system_tools.nft_plan('test')


if __name__ == '__main__':
    unittest.main()
