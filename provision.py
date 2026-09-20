#!/usr/bin/env python3
"""Online Xboard provisioning. No secrets in command arguments or diagnostics."""
import argparse
import copy
import errno
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


class PortError(ProvisionError):
    pass


def http_port_free():
    # Go's HTTP listener uses SO_REUSEADDR. TIME_WAIT is not a live owner.
    for family, address in ((socket.AF_INET, ('0.0.0.0', 80)), (socket.AF_INET6, ('::', 80))):
        try:
            with socket.socket(family, socket.SOCK_STREAM) as sock:
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                if family == socket.AF_INET6:
                    sock.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
                sock.bind(address)
        except OSError as exc:
            if family == socket.AF_INET6 and exc.errno in (errno.EAFNOSUPPORT, errno.EPROTONOSUPPORT, errno.EADDRNOTAVAIL):
                continue
            if exc.errno == errno.EADDRINUSE:
                return False
            raise PortError('无法检查TCP80绑定权限/地址；不会把权限错误当作端口占用') from None
    return True


def http_process_info(pid):
    base = Path('/proc') / str(pid)
    # Never read cmdline/environ: those can contain passwords.
    stat = (base / 'stat').read_text().rsplit(') ', 1)[1].split()
    groups = [line.split(':', 2)[2] for line in (base / 'cgroup').read_text().splitlines()]
    units = {part for group in groups for part in group.split('/') if part.endswith('.service')}
    unit = next(iter(units)) if len(units) == 1 else ''
    if unit and not any(group.startswith('/system.slice/') and group.endswith('/' + unit) for group in groups):
        raise PortError('TCP80由容器/用户服务/嵌套控制组占用，请在其管理器中停止或选择DNS证书')
    name = re.sub(r'[^A-Za-z0-9_.:+-]', '?', (base / 'comm').read_text().strip())[:64]
    if len(units) > 1 or any(re.search(r'docker|kubepods|containerd|libpod|lxc', group) for group in groups):
        raise PortError('不自动终止容器托管的TCP80进程；请停止指定容器或选择DNS证书')
    protected = {'systemd', 'init', 'sshd', 'ssh', 'dockerd', 'docker-proxy', 'containerd', 'runc', 'dbus-daemon', 'NetworkManager'}
    if pid <= 1 or pid in (os.getpid(), os.getppid()) or name in protected:
        raise PortError('TCP80由受保护的系统/会话进程占用，拒绝自动终止')
    if not unit and name.lower() in ('v2bx', 'v2bx-2'):
        raise PortError('检测到非托管V2bX进程，请先核对其节点，拒绝自动终止')
    return {'pid': pid, 'start': stat[19], 'name': name, 'unit': unit}


def http_listeners():
    lines = run('ss', '-H', '-lntp', 'sport = :80').stdout.splitlines()
    pids = set()
    for line in lines:
        found = re.findall(r'\bpid=(\d+)', line)
        if not found:
            raise PortError('TCP80占用者PID不可见，拒绝强制释放；请用root检查ss -lntp')
        pids.update(int(pid) for pid in found)
    try:
        return [http_process_info(pid) for pid in sorted(pids)]
    except (FileNotFoundError, ProcessLookupError):
        raise PortError('TCP80占用进程刚发生变化，请重新预检；未终止任何新进程') from None


def http_service_info(unit):
    if not re.fullmatch(r'[A-Za-z0-9_.@:-]+\.service', unit):
        raise PortError('无法安全识别TCP80所属服务名')
    if unit in ('ssh.service', 'sshd.service', 'docker.service', 'containerd.service', 'dbus.service') or unit.startswith('systemd-'):
        raise PortError('拒绝停止受保护的系统服务：' + unit)
    props = 'Id,LoadState,ActiveState,CanStop,RefuseManualStop,ControlGroup,TriggeredBy,KillMode'
    values = dict(line.split('=', 1) for line in run('systemctl', 'show', unit, '--property=' + props).stdout.splitlines() if '=' in line)
    if (values.get('Id') != unit or values.get('LoadState') != 'loaded' or values.get('ActiveState') != 'active'
            or values.get('CanStop') != 'yes' or values.get('RefuseManualStop') != 'no'
            or values.get('KillMode') not in ('control-group', 'mixed')
            or values.get('ControlGroup') != '/system.slice/' + unit):
        raise PortError('服务状态/控制组不适合自动释放，请人工检查：' + unit)
    if values.get('TriggeredBy'):
        raise PortError('服务有socket/timer等激活源，拒绝自动停止：' + unit + '；请人工处理或选择DNS证书')
    return values


class HTTPPortLease:
    def __init__(self, owners, services, allow_kill=False):
        self.owners, self.services, self.allow_kill = owners, services, allow_kill
        self.stopped = []

    @classmethod
    def prepare(cls, checks, ask, release=False, kill=False, interactive=False, check_only=False):
        if not any(check.get('http_challenge') for check in checks) or http_port_free():
            return None
        owners = http_listeners()
        if not owners:
            raise PortError('TCP80绑定冲突但未找到监听进程，可能是短暂竞态；请稍后重新预检')
        print('HTTP-01需要TCP80，检测到以下占用者（不显示进程参数）：')
        for owner in owners:
            print('  PID=%d  程序=%s  服务=%s' % (owner['pid'], owner['name'], owner['unit'] or '无systemd托管'))
        services = {o['unit']: http_service_info(o['unit']) for o in owners if o['unit'] and o['unit'] != 'V2bX.service'}
        raw = [o for o in owners if not o['unit']]
        if services and any(c.get('transport') == 'tcp' and c.get('port') == 80 for c in checks):
            raise PortError('新节点也需要TCP80，无法与待恢复服务共用；请改节点端口或人工迁移服务')
        if check_only:
            raise PortError('TCP80仍被占用；--check-only绝不停止/终止进程。部署时可明确选择临时释放')
        if services:
            print('临时停止：' + ', '.join(sorted(services)) + '。网站可能暂时中断；成功/失败/正常取消后恢复，不取消开机自启。')
            print('异常断电/SIGKILL后人工恢复：systemctl start ' + ' '.join(sorted(services)))
            print('注意：仅本次部署临时释放；将来HTTP自动续签仍需TCP80空闲，长期共用请采用DNS证书。')
            if not (release or kill):
                if not interactive or ask('是否临时释放TCP80？输入 Y 继续，N/回车取消', 'N').lower() != 'y':
                    raise PortError('已取消释放TCP80，原服务未修改')
        if raw:
            if not hasattr(os, 'pidfd_open') or not hasattr(signal, 'pidfd_send_signal'):
                raise PortError('内核/Python不支持安全PID句柄；独立进程请人工停止，不使用有PID复用风险的kill')
            print('危险：独立进程无法自动恢复。将先发送TERM，5秒后仍未释放才KILL；不会终止新出现的占用者。')
            if not kill:
                if not interactive or ask('确认强制终止上述独立进程，输入 KILL-PORT80', '') != 'KILL-PORT80':
                    raise PortError('未确认强制终止，原进程未修改')
        print('已记录释放授权；只有最终输入DEPLOY后才会执行，不立即关闭进程。')
        return cls(owners, services, bool(raw))

    def release(self):
        if http_port_free():
            return
        current = http_listeners()
        approved = {o['pid']: o for o in self.owners if o['unit'] != 'V2bX.service'}
        if not current or any(approved.get(o['pid']) != o for o in current):
            raise PortError('TCP80占用者已变化，取消释放；请重新预检，未终止新进程')
        for unit, expected in self.services.items():
            if any(o['unit'] == unit for o in current) and http_service_info(unit) != expected:
                raise PortError('TCP80服务状态已变化，取消释放：' + unit)
        handles = []
        try:
            for owner in current:
                if not owner['unit']:
                    if not self.allow_kill:
                        raise PortError('没有独立进程强制终止授权')
                    fd = os.pidfd_open(owner['pid'])
                    handles.append((owner, fd))
                    if http_process_info(owner['pid']) != owner:
                        raise PortError('TCP80 PID身份已变化，取消强制释放')
            for unit in sorted(self.services):
                if any(o['unit'] == unit for o in current):
                    # Record BEFORE stop: a timeout/nonzero stop may already have stopped it.
                    self.stopped.append(unit)
                    run('systemctl', 'stop', unit)
            for _, fd in handles:
                try:
                    signal.pidfd_send_signal(fd, signal.SIGTERM)
                except ProcessLookupError:
                    pass
            deadline = time.monotonic() + 5
            while not http_port_free() and time.monotonic() < deadline:
                time.sleep(0.2)
            if not http_port_free():
                remaining = http_listeners()
                if any(approved.get(o['pid']) != o for o in remaining):
                    raise PortError('TCP80出现新的占用者，停止部署；不会强杀新进程')
                remaining_pids = {o['pid'] for o in remaining}
                for owner, fd in handles:
                    if owner['pid'] not in remaining_pids:
                        continue
                    try:
                        signal.pidfd_send_signal(fd, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                deadline = time.monotonic() + 3
                while not http_port_free() and time.monotonic() < deadline:
                    time.sleep(0.2)
            if not http_port_free():
                raise PortError('TCP80未释放或被新进程占用，停止部署；不会循环杀进程')
            print('TCP80已释放，仅用于本次证书申请。')
        finally:
            for _, fd in handles:
                os.close(fd)

    def restore(self):
        errors = []
        for unit in reversed(self.stopped[:]):
            try:
                run('systemctl', 'start', unit)
                if run('systemctl', 'is-active', '--quiet', unit, check=False).returncode:
                    raise PortError('未恢复active')
                self.stopped.remove(unit)
                print('已恢复原服务：' + unit)
            except (OSError, ValueError, subprocess.SubprocessError):
                errors.append(unit)
        if errors:
            raise PortError('TCP80原服务恢复失败，请立即执行并核查：systemctl start ' + ' '.join(errors))


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


def preflight(document, public_ip=None, allow_empty=False, allow_insecure_panel=False, allow_busy_http=False):
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
            due = have_cert and mode == 'http' and run(
                'openssl', 'x509', '-in', cert['CertFile'], '-noout', '-checkend', str(31 * 86400), check=False).returncode != 0
            if mode == 'http' and (not have_cert or due):
                check['http_challenge'] = True
                addresses = {item[4][0] for item in socket.getaddrinfo(domain, 80, type=socket.SOCK_STREAM)}
                if public_ip and str(ipaddress.ip_address(public_ip)) not in addresses:
                    raise ProvisionError('域名未解析到指定公网IP')
                if any(':' in address for address in addresses):
                    local6 = run('ip', '-6', '-o', 'addr', 'show', 'scope', 'global').stdout
                    if any(address not in local6 for address in addresses if ':' in address):
                        raise ProvisionError('AAAA记录不对应本机IPv6，请修复后再申请证书')
                if not http_port_free() and not allow_busy_http:
                    raise ProvisionError('HTTP-01需要TCP80；当前被占用，请在在线指引中明确选择临时释放或使用DNS证书')
                print('HTTP-01域名预检通过；还需确认TCP80空闲/临时释放及云安全组放行。证书由内置lego申请/续期。')
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


def deploy(path, document, checks, expected, timeout, enable, port_lease=None):
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
        if port_lease is not None:
            port_lease.release()
        elif any(check.get('http_challenge') for check in checks) and not http_port_free():
            raise ProvisionError('部署前TCP80被新进程占用；未授权自动释放，取消部署')
        cfg.write_atomic(path, document, expected, notice=False)
        written, newdata = True, path.read_bytes()
        run('systemctl', 'reset-failed', SERVICE, check=False)
        run('systemctl', 'start', SERVICE)
        pid = wait_healthy(document, checks, timeout)
        if port_lease is not None:
            port_lease.restore()
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
        try:
            if port_lease is not None:
                port_lease.restore()
        finally:
            DROPIN.unlink(missing_ok=True)
            reload_result = run('systemctl', 'daemon-reload', check=False)
            if reload_result.returncode:
                print('警告：临时服务覆盖已移除，但systemd daemon-reload失败。', file=sys.stderr)


def reusable_certificate(existing, node, domain, mode, nodes):
    used = {n['CertConfig'].get(key) for n in nodes for key in ('CertFile', 'KeyFile')}
    for previous in (existing or {}).get('Nodes', []):
        if (previous.get('ApiHost', '').rstrip('/') != node['ApiHost'] or previous.get('NodeID') != node['NodeID']
                or previous.get('NodeType') != node['NodeType']):
            continue
        cert = previous.get('CertConfig', {})
        if cert.get('CertMode') != mode or cert.get('CertDomain', '').lower().rstrip('.') != domain.lower().rstrip('.'):
            continue
        paths = [Path(cert.get(key, '')) for key in ('CertFile', 'KeyFile')]
        if any(not p.is_absolute() or not p.is_file() or p.is_symlink() or p.resolve() != p or str(p) in used for p in paths):
            continue
        try:
            certificate_valid(cert)
        except (ValueError, OSError, subprocess.SubprocessError):
            print('已有证书未通过校验，不自动复用；请核对证书，避免重复申请。')
            continue
        print('已验证并复用该节点现有证书及ACME配置，不创建新的证书目录。')
        return copy.deepcopy(cert)
    return None


def wizard(existing=None):
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
            reused = reusable_certificate(existing, node, domain, mode, nodes) if mode != 'file' else None
            cert = reused or {'CertMode': mode, 'CertDomain': domain, 'CertFile': directory + '/fullchain.pem', 'KeyFile': directory + '/privkey.pem'}
            if mode == 'file':
                cert['CertFile'], cert['KeyFile'] = cfg.required('已有证书绝对路径'), cfg.required('已有私钥绝对路径')
            elif reused is None:
                cert['Email'] = cfg.ask('ACME联系邮箱（可空，不编造邮箱）', '')
                print("申请证书即同意Let's Encrypt服务条款。")
            if mode == 'dns' and reused is None:
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
    parser.add_argument('--release-http-port', action='store_true', help='明确允许本次签证书临时停止TCP80托管服务，完成后恢复')
    parser.add_argument('--kill-http-port', action='store_true', help='危险：允许TERM/KILL非托管TCP80占用者，无法自动恢复；同时允许临时停止托管服务')
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
            document, wizard_insecure = wizard(cfg.read_document(path) if expected is not None else None)
        allow_http = confirm_http_panel(document, args.allow_insecure_panel or wizard_insecure,
                                        not (args.yes or args.check_only))
        checks = preflight(document, args.public_ip, args.allow_empty, allow_http, allow_busy_http=True)
        port_lease = HTTPPortLease.prepare(checks, cfg.ask, release=args.release_http_port,
                                          kill=args.kill_http_port, interactive=not args.yes,
                                          check_only=args.check_only)
        if args.check_only:
            print('预检完成；未写入配置、申请证书或操作服务。')
            return
        print('将停止旧节点、备份替换配置、申请/校验证书并启动。失败回滚；勿重复尝试CA错误。')
        if not args.yes and cfg.ask('同意证书条款并部署？输入 DEPLOY', '') != 'DEPLOY':
            print('已取消，未更改服务和配置。')
            return
        deploy(path, document, checks, expected, args.timeout, not args.no_enable, port_lease)


if __name__ == '__main__':
    try:
        main()
    except (EOFError, KeyboardInterrupt):
        print('已中止。部署期间中断会执行回滚。', file=sys.stderr)
        sys.exit(1)
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        print('上线失败：' + str(exc), file=sys.stderr)
        sys.exit(1)
