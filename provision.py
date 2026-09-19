#!/usr/bin/env python3
"""Online Xboard provisioning. No secrets in command arguments or diagnostics."""
import argparse
try:
    import fcntl
except ImportError:  # Windows can still run the loopback/offline tests.
    fcntl = None
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import configure as cfg

SERVICE = 'V2bX.service'
DROPIN = Path('/run/systemd/system/V2bX.service.d/90-v2bx-provision.conf')
TYPES = ('hysteria', 'vless', 'vmess', 'trojan', 'shadowsocks', 'tuic', 'anytls', 'hysteria2')


class ProvisionError(ValueError):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def run(*args, check=True, timeout=30):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if check and result.returncode:
        raise ProvisionError('命令失败：' + args[0] + ' (exit ' + str(result.returncode) + ')')
    return result


def api_get(node, endpoint, node_type=None):
    query = urllib.parse.urlencode({'node_type': node_type or node['NodeType'],
                                   'node_id': node['NodeID'], 'token': node['ApiKey']})
    url = node['ApiHost'].rstrip('/') + '/api/v1/server/UniProxy/' + endpoint + '?' + query
    request = urllib.request.Request(url, headers={'Accept': 'application/json', 'User-Agent': 'V2bX-2-config'})
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=20) as response:
            body = response.read(16 * 1024 * 1024 + 1)
            if len(body) > 16 * 1024 * 1024:
                raise ProvisionError('面板响应超过16MiB限制')
            result = json.loads(body)
            if not isinstance(result, dict):
                raise ProvisionError('面板返回的不是JSON对象')
            return result
    except urllib.error.HTTPError as exc:
        status = exc.code
        exc.close()
        raise ProvisionError('面板 ' + endpoint + ' HTTP ' + str(status)) from None
    except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError):
        raise ProvisionError('面板 ' + endpoint + ' 连接或JSON解析失败（未输出含密钥URL）') from None


def needs_tls(info, protocol):
    return protocol in ('hysteria', 'hysteria2', 'trojan', 'tuic', 'anytls') or str(info.get('tls', 0)) == '1'


def effective_protocol(info, fallback):
    protocol = info.get('protocol') or fallback
    if protocol == 'hysteria' and str(info.get('version', 1)) == '2':
        return 'hysteria2'
    return protocol


def inspect_node(node):
    info = api_get(node, 'config')
    protocol = effective_protocol(info, node['NodeType'])
    if protocol not in cfg.PROTOCOLS[node['Core']]:
        raise ProvisionError('面板协议与所选核心不兼容')
    allowed = (node['NodeType'], 'hysteria2' if node['NodeType'] == 'hysteria' else node['NodeType'])
    if protocol not in allowed:
        raise ProvisionError('面板协议与NodeType不匹配')
    port = info.get('server_port')
    if isinstance(port, bool) or not str(port).isdigit() or not 1 <= int(port) <= 65535:
        raise ProvisionError('面板 server_port 必须为单个有效监听端口')
    users = api_get(node, 'user').get('users')
    if not isinstance(users, list):
        raise ProvisionError('面板没有返回合法 users 数组')
    return {'info': info, 'protocol': protocol, 'port': int(port), 'users': len(users),
            'transport': 'udp' if protocol in ('hysteria', 'hysteria2', 'tuic') else 'tcp'}


def certificate_valid(cert):
    certfile, keyfile = cert['CertFile'], cert['KeyFile']
    run('openssl', 'x509', '-in', certfile, '-noout', '-checkend', '86400')
    pub = run('openssl', 'x509', '-in', certfile, '-pubkey', '-noout').stdout.strip()
    key = run('openssl', 'pkey', '-in', keyfile, '-pubout').stdout.strip()
    if pub != key:
        raise ProvisionError('证书与私钥不匹配')
    if cert.get('CertMode') != 'self':
        run('openssl', 'verify', '-purpose', 'sslserver', '-verify_hostname', cert['CertDomain'],
            '-untrusted', certfile, certfile)


def preflight(document, public_ip=None, allow_empty=False, allow_insecure_panel=False):
    cfg.validate(document)
    checks, cert_paths = [], set()
    for node in document['Nodes']:
        if 'Include' in node or 'ApiConfig' in node or 'Options' in node or not node.get('Core'):
            raise ProvisionError('在线部署仅支持展开后的扁平节点配置；Include/嵌套配置请先人工展开')
        if node['ApiHost'].startswith('http://'):
            if not allow_insecure_panel:
                raise ProvisionError('在线部署拒绝HTTP面板；如确认受信任内网传输，请显式 --allow-insecure-panel')
            print('警告：指定面板使用HTTP，API密钥会明文传输；本次由显式参数允许。')
        check = inspect_node(node)
        print('面板验证成功：NodeID=%d 协议=%s %s/%d 授权用户=%d 跳跃范围=%s' % (
            node['NodeID'], check['protocol'], check['transport'].upper(), check['port'], check['users'],
            check['info'].get('ports') or '无'))
        if not check['users']:
            if not allow_empty or check['protocol'] != 'hysteria2':
                raise ProvisionError('该节点没有授权用户；仅Hysteria2可显式 --allow-empty 等待授权')
        cert = node.get('CertConfig', {})
        mode = cert.get('CertMode', 'none')
        if needs_tls(check['info'], check['protocol']):
            if mode not in ('http', 'dns', 'file', 'self'):
                raise ProvisionError('面板要求TLS，不能使用none证书模式')
            domain = cert.get('CertDomain', '')
            if not re.fullmatch(r'(?=.{1,253}$)[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?', domain) or '.' not in domain:
                raise ProvisionError('请设置有效的CertDomain')
            if '/' in cert.get('Email', '') or '\\' in cert.get('Email', ''):
                raise ProvisionError('邮箱含非法路径字符')
            for name in ('CertFile', 'KeyFile'):
                p = Path(cert.get(name, ''))
                if not p.is_absolute() or p.is_symlink() or p.resolve() != p:
                    raise ProvisionError('证书路径必须是无符号链接的绝对路径')
                if str(p) in cert_paths:
                    raise ProvisionError('在线多节点部署请使用每个节点独立的证书和私钥路径')
                cert_paths.add(str(p))
            have_cert, have_key = Path(cert['CertFile']).exists(), Path(cert['KeyFile']).exists()
            if have_cert != have_key:
                raise ProvisionError('只有证书或私钥之一，停止以避免重复申请；请人工修复')
            if have_cert:
                certificate_valid(cert)
            elif mode == 'file':
                raise ProvisionError('file模式证书不存在')
            elif mode == 'http':
                addresses = {item[4][0] for item in socket.getaddrinfo(domain, 80, type=socket.SOCK_STREAM)}
                if public_ip and str(ipaddress.ip_address(public_ip)) not in addresses:
                    raise ProvisionError('域名未解析到指定公网IP')
                if any(':' in address for address in addresses):
                    local6 = run('ip', '-6', '-o', 'addr', 'show', 'scope', 'global').stdout
                    if any(address not in local6 for address in addresses if ':' in address):
                        raise ProvisionError('AAAA记录不对应本机IPv6，请修复后再申请证书')
                with socket.socket() as sock:
                    try:
                        sock.bind(('0.0.0.0', 80))
                    except OSError:
                        raise ProvisionError('HTTP-01需要空闲TCP80，当前被占用；不会关闭其它服务') from None
                print('HTTP-01预检通过；仍需云安全组允许公网TCP80。证书由内置lego申请/续期。')
        checks.append(check)
    return checks


def safe_logs(document, invocation):
    if not invocation:
        return '(无本次进程日志)'
    logs = run('journalctl', '_SYSTEMD_INVOCATION_ID=' + invocation, '-n', '100', '--no-pager', '-o', 'cat', check=False).stdout
    for node in document['Nodes']:
        logs = logs.replace(node['ApiKey'], '[REDACTED]')
        for value in node.get('CertConfig', {}).get('DNSEnv', {}).values():
            if value:
                logs = logs.replace(value, '[REDACTED]')
    logs = re.sub(r'(?i)(token|api_key|password)=([^&\s]+)', r'\1=[REDACTED]', logs)
    return re.sub(r'(?i)\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b', '[USER-ID]', logs)


def wait_healthy(document, checks, timeout):
    deadline, stable, previous = time.monotonic() + timeout, None, None
    invocation = ''
    while time.monotonic() < deadline:
        values = run('systemctl', 'show', SERVICE, '-p', 'ActiveState', '-p', 'MainPID', '-p', 'InvocationID').stdout
        state = dict(line.split('=', 1) for line in values.splitlines() if '=' in line)
        invocation = state.get('InvocationID') or invocation
        pid = state.get('MainPID', '0')
        if state.get('ActiveState') in ('failed', 'inactive'):
            raise ProvisionError('服务提前退出。脱敏日志：\n' + safe_logs(document, invocation))
        if state.get('ActiveState') == 'active' and pid == '0':
            time.sleep(1)
            continue
        listeners = run('ss', '-H', '-lntup').stdout.splitlines()
        listening = all(any(line.startswith(c['transport']) and re.search(r':' + str(c['port']) + r'\s', line)
                            and 'pid=' + pid + ',' in line for line in listeners) for c in checks)
        ready = listening and pid != '0' and state.get('ActiveState') == 'active'
        if ready:
            if previous != pid:
                stable = time.monotonic()
            if stable is not None and time.monotonic() - stable >= 8:
                for node, check in zip(document['Nodes'], checks):
                    if needs_tls(check['info'], check['protocol']):
                        certificate_valid(node['CertConfig'])
                        os.chmod(node['CertConfig']['KeyFile'], 0o600)
                return pid
        else:
            stable = None
        previous = pid if ready else None
        time.sleep(1)
    raise ProvisionError('等待证书/监听/稳定进程超时。脱敏日志：\n' + safe_logs(document, invocation))


def restore_config_bytes(path, original, expected):
    # Restore the exact original, including supported JSON5 comments/formatting.
    if path.is_symlink() or path.read_bytes() != expected:
        raise ProvisionError('配置已被并发修改，拒绝回滚覆盖')
    fd, name = tempfile.mkstemp(prefix='.rollback-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as handle:
            handle.write(original)
            handle.flush()
            os.fsync(handle.fileno())
        if path.is_symlink() or path.read_bytes() != expected:
            raise ProvisionError('配置已被并发修改，拒绝回滚覆盖')
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def deploy(path, document, checks, expected, timeout, enable):
    active = run('systemctl', 'is-active', '--quiet', SERVICE, check=False).returncode == 0
    enabled = run('systemctl', 'is-enabled', '--quiet', SERVICE, check=False).returncode == 0
    execstart = run('systemctl', 'show', SERVICE, '-p', 'ExecStart', '--value').stdout
    if not re.search(r'--config[= ]' + re.escape(str(path)) + r'(?:[ ;}]|$)', execstart):
        raise ProvisionError('服务ExecStart未明确使用该配置路径，拒绝写入；请核对systemctl cat V2bX')
    if DROPIN.exists() or DROPIN.is_symlink():
        raise ProvisionError('存在未完成部署的临时服务覆盖文件，请先人工核查')
    DROPIN.parent.mkdir(parents=True, exist_ok=True)
    DROPIN.write_text('[Service]\nRestart=no\n', encoding='utf-8')
    written, stopped, newdata = False, False, None
    try:
        run('systemctl', 'daemon-reload')
        run('systemctl', 'stop', SERVICE)
        stopped = True
        cfg.write_atomic(path, document, expected, notice=False)
        written, newdata = True, path.read_bytes()
        run('systemctl', 'reset-failed', SERVICE, check=False)
        run('systemctl', 'start', SERVICE)
        pid = wait_healthy(document, checks, timeout)
        if enable:
            run('systemctl', 'enable', SERVICE)
        print('服务已稳定运行：PID=' + pid + '；证书、实际监听和面板读取通过。')
        if any(not check['users'] for check in checks):
            print('空用户节点尚未授权任何面板用户，等待面板分配；不代表代理已可用。')
        print('尚需公网客户端验证认证、UDP范围/云安全组和面板流量入账。')
    except BaseException as original:
        if stopped:
            run('systemctl', 'stop', SERVICE, check=False)
        rollback_error = None
        restore_error = None
        if written:
            try:
                if not path.exists() or path.read_bytes() != newdata:
                    raise ProvisionError('失败时发现并发配置修改，已停止服务；拒绝覆盖管理员更改')
                if expected is None:
                    path.unlink()
                else:
                    restore_config_bytes(path, expected, newdata)
            except BaseException as error:
                rollback_error = error
        if not enabled:
            run('systemctl', 'disable', SERVICE, check=False)
        if stopped and active and rollback_error is None:
            try:
                restored = run('systemctl', 'start', SERVICE, check=False)
                if restored.returncode:
                    restore_error = ProvisionError('旧服务恢复启动失败')
            except BaseException as error:
                restore_error = error
        if rollback_error or restore_error:
            details = []
            if rollback_error:
                details.append('配置回滚失败：' + str(rollback_error))
            if restore_error:
                details.append('旧服务恢复失败：' + str(restore_error))
            raise ProvisionError('部署失败且需要人工处理；' + '；'.join(details)) from original
        print('部署失败，已回滚配置；新签发证书保留以避免CA限流。', file=sys.stderr)
        raise
    finally:
        DROPIN.unlink(missing_ok=True)
        reload_result = run('systemctl', 'daemon-reload', check=False)
        if reload_result.returncode:
            print('警告：临时服务覆盖已移除，但systemd daemon-reload失败。', file=sys.stderr)


def wizard():
    print('在线配置：真实验证面板，申请证书，启动并检查服务；不会创建面板用户。')
    nodes, cores, insecure_panel = [], {}, False
    while True:
        core = cfg.choose('核心', tuple(cfg.PROTOCOLS), 'sing')
        api_host = cfg.required('面板地址').rstrip('/')
        if api_host.startswith('http://'):
            if cfg.ask('HTTP面板会明文传输密钥，输入 INSECURE-HTTP 继续', '') != 'INSECURE-HTTP':
                raise ProvisionError('未确认HTTP面板风险')
            insecure_panel = True
        node = {'Core': core, 'ApiHost': api_host,
                'ApiKey': cfg.required('面板密钥（不回显）', secret=True),
                'NodeID': int(cfg.required('节点ID')), 'NodeType': 'hysteria',
                'Timeout': 30, 'ListenIP': '0.0.0.0', 'SendIP': '0.0.0.0'}
        if not cfg.valid_host(node['ApiHost']) or node['NodeID'] <= 0:
            raise ProvisionError('面板地址/节点ID无效')
        selected = cfg.choose('面板协议（auto探测API）', ('auto',) + TYPES, 'auto')
        info = None
        for kind in TYPES if selected == 'auto' else (selected,):
            try:
                candidate = api_get(node, 'config', kind)
                if effective_protocol(candidate, kind) not in cfg.PROTOCOLS[core]:
                    continue
                info, node['NodeType'] = candidate, kind
                break
            except ProvisionError:
                if selected != 'auto':
                    raise
        if info is None:
            raise ProvisionError('无法识别节点，请核对密钥、节点ID与面板协议')
        protocol = effective_protocol(info, node['NodeType'])
        print('识别到协议：' + protocol)
        if needs_tls(info, protocol):
            domain = cfg.ask('证书域名', info.get('server_name') or info.get('host') or '')
            mode = cfg.choose('证书方式', ('http', 'dns', 'file'), 'http')
            directory = '/etc/V2bX/certs/node-' + str(node['NodeID']) + '-' + str(len(nodes) + 1)
            cert = {'CertMode': mode, 'CertDomain': domain, 'CertFile': directory + '/fullchain.pem', 'KeyFile': directory + '/privkey.pem'}
            if mode == 'file':
                cert['CertFile'], cert['KeyFile'] = cfg.required('已有证书绝对路径'), cfg.required('已有私钥绝对路径')
            else:
                cert['Email'] = cfg.ask('ACME联系邮箱（可空，不编造邮箱）', '')
                print("申请证书即同意Let's Encrypt服务条款。")
            if mode == 'dns':
                cert['Provider'], cert['DNSEnv'] = cfg.required('lego DNS Provider'), {}
                while True:
                    name = cfg.ask('DNS凭据变量名（空结束）', '')
                    if not name:
                        break
                    if not re.fullmatch(r'[A-Z][A-Z0-9_]*', name):
                        raise ProvisionError('DNS环境变量名无效')
                    cert['DNSEnv'][name] = cfg.required('变量值', secret=True)
                if not cert['DNSEnv']:
                    raise ProvisionError('DNS凭据未填写')
            node['CertConfig'] = cert
        else:
            node['CertConfig'] = {'CertMode': 'none'}
            print('面板未启用TLS或使用REALITY，不申请公共证书。')
        cores[core] = {'Type': core, 'Log': {'Level': 'info'}}
        nodes.append(node)
        if cfg.choose('是否继续添加节点？', ('y', 'n'), 'n') == 'n':
            return {'Log': {'Level': 'info'}, 'Cores': list(cores.values()), 'Nodes': nodes}, insecure_panel


def confirm_http_panel(document, allowed=False, interactive=False):
    if allowed or not any(node.get('ApiHost', '').startswith('http://') for node in document.get('Nodes', [])):
        return allowed
    if interactive:
        return cfg.ask('HTTP面板会明文传输密钥，输入 INSECURE-HTTP 继续', '') == 'INSECURE-HTTP'
    return False


def main():
    parser = argparse.ArgumentParser(description='V2bX-2在线配置与上线验证')
    parser.add_argument('--config', default='/etc/V2bX/config.json')
    parser.add_argument('--from-file', help='0600 JSON配置，不把密钥放命令行')
    parser.add_argument('--edit', help='编辑私有副本，在线预检后部署')
    parser.add_argument('--public-ip', help='验证HTTP证书域名解析')
    parser.add_argument('--allow-empty', action='store_true')
    parser.add_argument('--allow-insecure-panel', action='store_true', help='显式允许HTTP面板（密钥明文传输）')
    parser.add_argument('--yes', action='store_true', help='确认停服务/替换/证书条款/启动')
    parser.add_argument('--no-enable', action='store_true')
    parser.add_argument('--check-only', action='store_true')
    parser.add_argument('--timeout', type=int, default=240)
    args = parser.parse_args()
    if os.name != 'posix' or fcntl is None or os.geteuid() != 0 or not Path('/run/systemd/system').is_dir():
        raise ProvisionError('在线部署需要root和运行中的systemd；离线请使用configure.py')
    if not 15 <= args.timeout <= 900:
        raise ProvisionError('等待时间必须为15..900秒')
    for command in ('systemctl', 'journalctl', 'ss', 'openssl', 'ip'):
        if not shutil.which(command):
            raise ProvisionError('缺少工具：' + command)
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    path = cfg.safe_path(Path(args.config), None)
    with open('/run/lock/v2bx-config.lock', 'a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ProvisionError('另一个配置部署正在运行') from None
        expected = path.read_bytes() if path.exists() else None
        wizard_insecure = False
        if args.from_file:
            source = Path(args.from_file)
            if source.is_symlink() or not source.is_file() or source.stat().st_mode & 0o077 or source.stat().st_uid != 0:
                raise ProvisionError('--from-file必须是root拥有的0600普通文件')
            document = cfg.read_document(source)
        elif args.edit:
            if expected is None:
                raise ProvisionError('缺少配置，请先执行v2bx generate')
            with tempfile.TemporaryDirectory(prefix='v2bx-online-edit-') as folder:
                draft = Path(folder) / 'config.json'
                draft.write_bytes(expected)
                draft.chmod(0o600)
                subprocess.run(shlex.split(args.edit) + [str(draft)], check=True)
                document = cfg.read_document(draft)
        else:
            document, wizard_insecure = wizard()
        allow_http = confirm_http_panel(document, args.allow_insecure_panel or wizard_insecure,
                                        not (args.yes or args.check_only))
        checks = preflight(document, args.public_ip, args.allow_empty, allow_http)
        if args.check_only:
            print('预检完成；未写入配置、申请证书或操作服务。')
            return
        print('将停止旧节点、备份替换配置、申请/校验证书并启动。失败回滚；勿重复尝试CA错误。')
        if not args.yes and cfg.ask('同意证书条款并部署？输入 DEPLOY', '') != 'DEPLOY':
            print('已取消，未更改服务和配置。')
            return
        deploy(path, document, checks, expected, args.timeout, not args.no_enable)


if __name__ == '__main__':
    try:
        main()
    except (EOFError, KeyboardInterrupt):
        print('已中止。部署期间中断会执行回滚。', file=sys.stderr)
        sys.exit(1)
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        print('上线失败：' + str(exc), file=sys.stderr)
        sys.exit(1)
