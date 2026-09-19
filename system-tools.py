#!/usr/bin/env python3
"""Explicit opt-in host maintenance; never run during ordinary node installation."""
import argparse
from contextlib import contextmanager
try:
    import fcntl
except ImportError:
    fcntl = None
import datetime
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile
import uuid


def run(*args, check=True):
    result = subprocess.run(args, capture_output=True, text=True, env=dict(os.environ, LC_ALL='C'))
    if check and result.returncode:
        raise RuntimeError('命令失败：' + ' '.join(args[:3]) + '\n' + result.stderr[-1500:])
    return result


def confirm(message, token):
    return input(message + '\n输入 ' + token + ' 确认，其他输入取消：').strip() == token


def backup_dir():
    directory = Path('/var/backups/V2bX-2') / (datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + uuid.uuid4().hex[:8])
    directory.mkdir(parents=True, mode=0o700)
    return directory


def atomic_bytes(path, data):
    if path.is_symlink():
        raise RuntimeError('拒绝覆盖符号链接：' + str(path))
    fd, temporary = tempfile.mkstemp(prefix='.v2bx-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        if path.is_symlink():
            raise RuntimeError('目标被修改为符号链接')
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@contextmanager
def state_lock(statefile):
    if fcntl is None:
        raise RuntimeError('此操作需要Linux flock')
    with open(str(statefile) + '.lock', 'a') as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        yield


def commit_rules(statefile):
    with state_lock(statefile):
        if not statefile.exists():
            raise RuntimeError('已超过回滚时间，不能宣称规则已保留；请重新核查')
        state = json.loads(statefile.read_text())
        state['committed'] = True
        atomic_bytes(statefile, json.dumps(state).encode())


def bbr():
    current = run('sysctl', '-n', 'net.ipv4.tcp_congestion_control').stdout.strip()
    print('当前内核：' + platform.release() + '；拥塞控制：' + current)
    print('“最新内核”指当前发行版已配置签名软件源的最新候选包，不是未知第三方/mainline。保留旧内核，不自动重启。')
    if not confirm('安装发行版内核元包并启用fq/BBR？自定义内核用户请先确认供应商兼容性。', 'INSTALL-KERNEL'):
        print('已取消，系统未修改。')
        return
    if shutil.which('apt-get'):
        info = dict(line.split('=', 1) for line in Path('/etc/os-release').read_text().splitlines() if '=' in line)
        distro = info.get('ID', '').strip('"')
        arch = run('dpkg', '--print-architecture').stdout.strip()
        if distro == 'debian' and arch in ('amd64', 'arm64'):
            package = 'linux-image-' + arch
        elif distro == 'ubuntu':
            package = 'linux-generic'
        else:
            raise RuntimeError('未验证的apt发行版，拒绝猜测内核元包')
        run('apt-get', 'update')
        print(run('apt-cache', 'policy', package).stdout)
        if not confirm('确认以上候选内核？下载和安装可能需要数分钟。', 'INSTALL'):
            print('已取消安装；仅刷新了软件源索引。')
            return
        os.environ['DEBIAN_FRONTEND'] = 'noninteractive'
        run('apt-get', 'install', '-y', '--no-install-recommends', package)
    elif shutil.which('dnf'):
        if not confirm('使用dnf安装已配置签名软件源的kernel包？', 'INSTALL'):
            return
        run('dnf', 'install', '-y', 'kernel')
    else:
        raise RuntimeError('仅自动支持Debian/Ubuntu/dnf，未执行第三方脚本')
    run('modprobe', 'tcp_bbr', check=False)
    available = run('sysctl', '-n', 'net.ipv4.tcp_available_congestion_control').stdout.split()
    if 'bbr' not in available:
        print('内核已安装；当前运行内核不支持BBR。请安排重启后再启用，未假报启用成功。')
        return
    destination = Path('/etc/sysctl.d/99-v2bx-bbr.conf')
    if destination.is_symlink():
        raise RuntimeError('sysctl目标为符号链接，拒绝覆盖')
    backup = backup_dir()
    old = destination.read_bytes() if destination.exists() else None
    if old is not None:
        (backup / '99-v2bx-bbr.conf').write_bytes(old)
    previous_qdisc = run('sysctl', '-n', 'net.core.default_qdisc').stdout.strip()
    try:
        atomic_bytes(destination, b'net.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n')
        run('sysctl', '-p', str(destination))
        if (run('sysctl', '-n', 'net.ipv4.tcp_congestion_control').stdout.strip() != 'bbr'
                or run('sysctl', '-n', 'net.core.default_qdisc').stdout.strip() != 'fq'):
            raise RuntimeError('BBR/fq实际值核验失败')
    except BaseException:
        if old is None:
            destination.unlink(missing_ok=True)
        else:
            atomic_bytes(destination, old)
        run('sysctl', '-w', 'net.core.default_qdisc=' + previous_qdisc, check=False)
        run('sysctl', '-w', 'net.ipv4.tcp_congestion_control=' + current, check=False)
        raise
    print('当前BBR已核验启用。备份：' + str(backup) + '；新内核是否生效需重启后核验uname -r。')


def nft_plan(tag):
    ruleset = json.loads(run('nft', '-j', 'list', 'ruleset').stdout)
    chains = []
    for item in ruleset.get('nftables', []):
        chain = item.get('chain', {})
        if chain.get('hook') in ('ingress', 'prerouting') and chain.get('type') != 'nat':
            raise RuntimeError('存在前置过滤链；请人工审查，不会宣称已放行所有端口')
        if chain.get('hook') == 'input':
            if chain.get('family') not in ('inet', 'ip', 'ip6') or chain.get('type') != 'filter':
                raise RuntimeError('发现不支持的input链')
            chains.append(chain)
    # Each base chain must accept: an ACCEPT in a new table cannot override later DROPs.
    script = ''.join('insert rule ' + c['family'] + ' ' + json.dumps(c['table']) + ' ' + json.dumps(c['name'])
                     + ' meta l4proto { tcp, udp } accept comment ' + json.dumps(tag) + '\n' for c in chains)
    return ruleset, script


def rollback(statefile, scheduled=False):
    path = Path(statefile)
    with state_lock(path):
        if not path.exists():
            return
        state = json.loads(path.read_text())
        if scheduled and state.get('committed'):
            return
        rollback_locked(path, state)


def rollback_locked(path, state):
    errors = []
    if state['backend'] == 'nft':
        data = json.loads(run('nft', '-j', 'list', 'ruleset').stdout)
        for item in data.get('nftables', []):
            rule = item.get('rule', {})
            if rule.get('comment') == state['tag']:
                result = run('nft', 'delete', 'rule', rule['family'], rule['table'], rule['chain'], 'handle', str(rule['handle']), check=False)
                if result.returncode:
                    errors.append(result.stderr)
    elif state['backend'] == 'iptables':
        for tool, spec in reversed(state['rules']):
            if run(tool, '-w', '5', '-C', 'INPUT', *spec, check=False).returncode == 0:
                result = run(tool, '-w', '5', '-D', 'INPUT', *spec, check=False)
                if result.returncode:
                    errors.append(result.stderr)
    if errors:
        raise RuntimeError('撤销失败，保留状态文件请人工处理：' + str(path))
    path.unlink(missing_ok=True)
    print('仅撤销本次新增放行规则，其他规则/NAT保持。')


def open_ports():
    print('危险：此操作向所有来源开放本机全部TCP/UDP入站端口，会暴露数据库等服务。')
    print('不改变云安全组、路由器或SELinux；不flush、不停防火墙、不删除Hysteria2 NAT。')
    print('只修改宿主输入过滤。原生规则重启/防火墙reload后可能失效，不伪称永久放行。')
    if not confirm('确认开放所有端口？正常节点只需放行指定UDP范围和HTTP-01 TCP80。', 'OPEN-ALL-PORTS'):
        print('已取消，防火墙未修改。')
        return
    for manager in ('firewalld', 'ufw'):
        if run('systemctl', 'is-active', '--quiet', manager, check=False).returncode == 0:
            raise RuntimeError(manager + '正在管理规则，请在其管理界面操作；不绕过或关闭防火墙管理器')
    if shutil.which('ufw') and 'Status: active' in run('ufw', 'status', check=False).stdout:
        raise RuntimeError('UFW处于启用状态，请先通过UFW审查规则；不会关闭UFW')
    directory = backup_dir()
    tag = 'v2bx-open-' + uuid.uuid4().hex[:12]
    state = {'tag': tag, 'rules': []}
    if shutil.which('nft'):
        data, script = nft_plan(tag)
        # Legacy iptables filters are separate from nft and must not be overlooked.
        for tool in ('iptables-legacy-save', 'ip6tables-legacy-save'):
            if shutil.which(tool):
                legacy = run(tool, check=False).stdout
                if '-A ' in legacy or 'INPUT DROP' in legacy:
                    raise RuntimeError('混合legacy/nft防火墙，需要人工审查')
        (directory / 'nft-ruleset.json').write_text(json.dumps(data, indent=2))
        (directory / 'nft-ruleset.txt').write_text(run('nft', 'list', 'ruleset').stdout)
        if not script:
            print('没有主机INPUT过滤链：本机未拦截TCP/UDP；仍需核对云安全组。未改规则。')
            return
        batch = directory / 'allow.nft'
        batch.write_text(script)
        run('nft', '--check', '-f', str(batch))
        state['backend'] = 'nft'
    elif shutil.which('iptables'):
        if Path('/proc/net/if_inet6').read_text().strip() and not shutil.which('ip6tables'):
            raise RuntimeError('IPv6已启用但缺少ip6tables，拒绝只放行IPv4')
        state['backend'] = 'iptables'
        for tool in ('iptables', 'ip6tables'):
            if shutil.which(tool):
                saved = run(tool + '-save').stdout
                table = ''
                for line in saved.splitlines():
                    if line.startswith('*'):
                        table = line[1:]
                    elif table in ('raw', 'mangle') and (line.startswith('-A ') or ' DROP ' in line):
                        raise RuntimeError('存在raw/mangle规则，请人工核查前置过滤')
                (directory / (tool + '.rules')).write_text(saved)
                for protocol in ('tcp', 'udp'):
                    state['rules'].append([tool, ['-p', protocol, '-m', 'comment', '--comment', tag, '-j', 'ACCEPT']])
    else:
        raise RuntimeError('没有nft/iptables，无法核验放行情况')
    statefile = directory / 'rollback.json'
    statefile.write_text(json.dumps(state))
    statefile.chmod(0o600)
    helper = directory / 'system-tools.py'
    shutil.copyfile(__file__, helper)
    # Scheduled before edits; survives SSH disconnection and Ctrl-C.
    run('systemd-run', '--unit=' + tag, '--on-active=120s', '--timer-property=AccuracySec=1s',
        sys.executable, str(helper), 'rollback', str(statefile), '--scheduled')
    try:
        with state_lock(statefile):
            if not statefile.exists():
                raise RuntimeError('回滚计时已结束，未应用规则')
            if state['backend'] == 'nft':
                run('nft', '-f', str(batch))
            else:
                for tool, spec in state['rules']:
                    run(tool, '-w', '5', '-I', 'INPUT', '1', *spec)
        print('已临时开放。120秒内未确认会自动撤回本工具新增规则。请从第二个SSH会话确认连接。')
        if confirm('确认保留新增规则？', 'KEEP'):
            commit_rules(statefile)
            run('systemctl', 'stop', tag + '.timer')
            print('已保留临时规则。备份及手动撤回状态：' + str(statefile))
            print('手动撤回：python3 ' + str(helper) + ' rollback ' + str(statefile))
        else:
            run('systemctl', 'stop', tag + '.timer', check=False)
            rollback(statefile)
    except BaseException:
        # Also roll back immediately if still connected; the timer is a safety net.
        if statefile.exists():
            rollback(statefile)
        raise


def main():
    parser = argparse.ArgumentParser(description='V2bX-2系统工具（显式危险确认）')
    parser.add_argument('action', choices=('bbr', 'open-ports', 'rollback'))
    parser.add_argument('state', nargs='?')
    parser.add_argument('--scheduled', action='store_true')
    args = parser.parse_args()
    if os.geteuid() != 0 or not Path('/run/systemd/system').is_dir():
        raise RuntimeError('需要root和systemd，隔离模式禁止宿主维护')
    if args.action == 'bbr':
        bbr()
    elif args.action == 'open-ports':
        open_ports()
    elif args.state:
        rollback(args.state, args.scheduled)
    else:
        raise RuntimeError('缺少撤回状态文件')


if __name__ == '__main__':
    try:
        main()
    except (Exception, KeyboardInterrupt) as exc:
        print('操作未完成：' + str(exc), file=sys.stderr)
        sys.exit(1)
