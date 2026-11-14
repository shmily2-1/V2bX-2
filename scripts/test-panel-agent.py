#!/usr/bin/env python3
"""Real loopback HTTP checks for native machine routes and legacy fallback."""
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODULE = Path(__file__).with_name('xboard-service-agent.py')
spec = importlib.util.spec_from_file_location('panel_agent', MODULE)
agent_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(agent_module)

class AgentRoutesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='v2bx-panel-agent-')
        self.addCleanup(self.temp.cleanup)
        self.paths = []
        self.status = 200
        owner = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass
            def do_POST(self):
                owner.paths.append(self.path)
                data = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                assert data['machine_id'] == 41 and data['token'] == 'isolated-test-token'
                self.send_response(owner.status if '/services/' in self.path else 200)
                self.end_headers()
                self.wfile.write(b'{"contract":1,"services":[]}')
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.config = {'panel': 'http://127.0.0.1:' + str(self.server.server_port),
                       'machine_id': 41, 'token': 'isolated-test-token',
                       'state_dir': self.temp.name, 'v2bx': '/unused', 'v2bx_config': '/unused'}

    def test_native_machine_resource_is_preferred(self):
        agent_module.Agent(self.config).sync()
        self.assertEqual(self.paths, ['/api/v2/server/machine/services/pull'])

    def test_only_missing_native_resource_falls_back(self):
        self.status = 404
        agent_module.Agent(self.config).sync()
        self.assertEqual(self.paths, ['/api/v2/server/machine/services/pull', '/api/v2/server/machine/v2bx/pull'])

    def test_auth_failure_does_not_fall_back_or_lose_pending_traffic(self):
        agent = agent_module.Agent(self.config)
        batch = {'service_id': 7, 'batch_id': 'isolated-batch', 'traffic': {}}
        agent.state['pending'] = [batch]
        agent.persist()
        self.status = 403
        with self.assertRaisesRegex(RuntimeError, 'HTTP 403'):
            agent.sync()
        self.assertEqual(self.paths, ['/api/v2/server/machine/services/meter'])
        self.assertEqual(agent_module.Agent(self.config).state['pending'], [batch])

if __name__ == '__main__':
    unittest.main(verbosity=2)
