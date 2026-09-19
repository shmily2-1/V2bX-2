#!/usr/bin/env python3
"""Offline multi-node config wizard; no panel/ACME/network/service actions.
Adapted from wyx2685/V2bX-script's configuration flow, MPL-2.0.
"""
import argparse
import datetime
import getpass
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit

PROTOCOLS = {
    'xray': ('vmess', 'vless', 'trojan', 'shadowsocks'),
    'sing': ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria', 'hysteria2', 'tuic', 'anytls'),
    'hysteria2': ('hysteria2',),
}


def ask(prompt, default=None, secret=False):
    label = prompt + (f' [{default}]' if default is not None else '') + ': '
    if secret and sys.stdin.isatty():
        value = getpass.getpass(label)
    else:
        value = input(label)
    if not secret:
        value = value.strip()
    if not value and default is not None:
        return default
    return value


def choose(prompt, values, default):
    while True:
        value = ask(prompt + ' (' + '/'.join(values) + ')', default)
        if value in values:
            return value
        print('无效选项，请重试。')


def required(prompt, secret=False):
    while True:
        value = ask(prompt, secret=secret)
        if value:
            return value
        print('此项不能为空。')


def valid_host(value):
    if not isinstance(value, str):
        return False
    parsed = urlsplit(value)
    return (parsed.scheme in ('https', 'http') and bool(parsed.hostname)
            and not parsed.username and not parsed.password and not parsed.query
            and not parsed.fragment and not any(c.isspace() for c in value))


def validate(document):
    """Structural check only; not an assertion of core/panel/certificate validity."""
    if not isinstance(document, dict):
        raise ValueError('顶层必须是 JSON 对象')
    cores = document.get('Cores')
    nodes = document.get('Nodes')
    if not isinstance(cores, list) or not cores or not isinstance(nodes, list) or not nodes:
        raise ValueError('Cores/Nodes 必须是非空数组')
    available = set()
    for core in cores:
        if not isinstance(core, dict) or core.get('Type') not in PROTOCOLS:
            raise ValueError('无效的核心 Type')
        available.add(core['Type'])
    for node in nodes:
        if not isinstance(node, dict):
            raise ValueError('节点必须是 JSON 对象')
        if node.get('Include'):
            # Do not fetch external or private includes during offline validation.
            continue
        api = node.get('ApiConfig', node)
        options = node.get('Options', node)
        if not isinstance(api, dict) or not isinstance(options, dict):
            raise ValueError('ApiConfig/Options 必须是对象')
        if options.get('Core') and options['Core'] not in available:
            raise ValueError('节点引用了未声明的核心')
        node_id = api.get('NodeID')
        if type(node_id) is not int or not 0 < node_id <= 2147483647:
            raise ValueError('NodeID 必须是 1..2147483647 的整数')
        if not valid_host(api.get('ApiHost')):
            raise ValueError('ApiHost 必须是无账号/密码/query/fragment 的 http(s) URL')
        key = api.get('ApiKey')
        if not isinstance(key, str) or key in ('', 'test', 'REPLACE_WITH_YOUR_PANEL_KEY'):
            raise ValueError('ApiKey 为空或仍是示例占位值')
        if api.get('NodeType') not in ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria', 'hysteria2', 'tuic', 'anytls', 'v2ray'):
            raise ValueError('NodeType 无效')
        cert = options.get('CertConfig', {}) or {}
        if not isinstance(cert, dict) or cert.get('CertMode', '') not in ('', 'none', 'file', 'self', 'http', 'dns'):
            raise ValueError('CertMode 无效')
        if cert.get('CertMode') in ('file', 'self'):
            if not cert.get('CertFile') or not cert.get('KeyFile'):
                raise ValueError('文件证书模式需 CertFile/KeyFile')
    return len(nodes)


def read_document(path):
    # Do not include JSON text or keys in exception output.
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except json.JSONDecodeError as exc:
        raise ValueError(f'JSON 语法错误，行 {exc.lineno} 列 {exc.colno}；此工具仅编辑标准 JSON') from None


def cert_config(protocol):
    default = 'file' if protocol in ('trojan', 'hysteria', 'hysteria2', 'tuic', 'anytls') else 'none'
    print('REALITY/无 TLS 请选择 none；file=已有证书；http/dns 仅写配置，启动核心后才可能申请证书。')
    mode = choose('证书模式', ('none', 'file', 'http', 'dns'), default)
    if mode == 'none':
        return {'CertMode': 'none'}
    domain = required('节点证书域名')
    if not re.fullmatch(r'[A-Za-z0-9.-]+', domain):
        raise ValueError('证书域名格式不正确')
    result = {'CertMode': mode, 'CertDomain': domain, 'RejectUnknownSni': False}
    for field, prompt, default in (
        ('CertFile', '证书文件绝对路径', '/etc/V2bX/fullchain.cer'),
        ('KeyFile', '私钥文件绝对路径', '/etc/V2bX/cert.key'),
    ):
        value = ask(prompt, default)
        if not value.startswith('/') or '\n' in value:
            raise ValueError('证书/私钥必须使用绝对路径')
        result[field] = value
    if mode in ('http', 'dns'):
        result['Email'] = required('ACME 联系邮箱')
        print('请确认域名解析/端口或 DNS 权限；向导不申请证书。')
    if mode == 'dns':
        result['Provider'] = ask('DNS Provider', 'cloudflare')
        result['DNSEnv'] = {}
        while True:
            name = ask('DNS 凭据环境变量名（留空结束）', '')
            if not name:
                break
            if not re.fullmatch(r'[A-Z][A-Z0-9_]*', name):
                raise ValueError('DNS 环境变量名不正确')
            result['DNSEnv'][name] = required('环境变量值（不回显）', secret=True)
        if not result['DNSEnv']:
            raise ValueError('dns 模式必须填写对应 Provider 的凭据环境变量')
    return result


def generate_document():
    print('V2bX-2 多节点配置向导：不连接面板、不申请证书、不启动节点。')
    cores, nodes = {}, []
    while True:
        core = choose('核心', tuple(PROTOCOLS), 'sing')
        raw_id = required('节点 Node ID')
        if not re.fullmatch(r'[0-9]+', raw_id) or not 0 < int(raw_id) <= 2147483647:
            raise ValueError('Node ID 必须为 1..2147483647')
        host = required('面板 ApiHost').rstrip('/')
        if not valid_host(host) or urlsplit(host).hostname == 'panel.example.com':
            raise ValueError('请使用真实面板 http(s) 地址，不要使用示例占位值')
        if host.startswith('http://'):
            print('警告：http 不加密面板密钥，请只用于可信网络。')
        key = required('面板 ApiKey（不回显）', secret=True)
        if key in ('test', 'REPLACE_WITH_YOUR_PANEL_KEY'):
            raise ValueError('不能使用示例 ApiKey')
        protocol = 'hysteria2' if core == 'hysteria2' else choose('协议', PROTOCOLS[core], 'vless')
        node_type = protocol
        if protocol == 'hysteria2':
            panel = choose('Hysteria2 面板 API', ('xboard', 'hysteria2'), 'xboard')
            node_type = 'hysteria' if panel == 'xboard' else 'hysteria2'
            print('Xboard 需设置 version=2，跳跃范围由面板下发；不要把公网范围写为监听端口。')
        listen = ask('监听 IP', '0.0.0.0')
        send = ask('出站 IP', '0.0.0.0')
        ipaddress.ip_address(listen)
        ipaddress.ip_address(send)
        node = {
            'Core': core, 'ApiHost': host, 'ApiKey': key, 'NodeID': int(raw_id),
            'NodeType': node_type, 'Timeout': 30, 'ListenIP': listen, 'SendIP': send,
            'DeviceOnlineMinTraffic': 200, 'ReportMinTraffic': 0,
            'CertConfig': cert_config(protocol),
        }
        if core == 'sing':
            node.update(EnableTFO=False, EnableSniff=True)
        if core == 'xray':
            node.update(EnableTFO=False, EnableDNS=False)
        cores[core] = {'Type': core, 'Log': {'Level': 'info' if core != 'xray' else 'warning'}}
        # No legacy sing-box DNS/route schema or unintended ACL/YAML generated.
        nodes.append(node)
        if choose('继续添加节点', ('y', 'n'), 'n') == 'n':
            break
    result = {'Log': {'Level': 'info'}, 'Cores': list(cores.values()), 'Nodes': nodes}
    validate(result)
    return result


def safe_path(path, root):
    path = path.absolute()
    if path.is_symlink():
        raise ValueError('配置文件是符号链接；请人工检查实际路径')
    resolved = path.resolve()
    if root and root != resolved and root not in resolved.parents:
        raise ValueError('配置路径逃逸隔离根目录')
    return path


def write_atomic(path, document, expected, notice=True):
    # Check before and just before rename; avoid overwriting a concurrent admin edit.
    current = path.read_bytes() if path.exists() else None
    if path.is_symlink() or current != expected:
        raise ValueError('配置在操作过程中被修改，已取消')
    data = (json.dumps(document, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    path.parent.mkdir(mode=0o750, parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.config.json.', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, 0o600)
        if expected is not None:
            stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
            bfd, backup = tempfile.mkstemp(prefix='config.json.backup.' + stamp + '.', dir=path.parent)
            with os.fdopen(bfd, 'wb') as handle:
                handle.write(expected)
            os.chmod(backup, 0o600)
            print('旧配置已备份（0600）：', backup)
        current = path.read_bytes() if path.exists() else None
        if path.is_symlink() or current != expected:
            raise ValueError('配置并发修改，未替换原文件')
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print('配置已原子写入（0600）：', path)
    if notice:
        print('未执行重启。注意运行中的核心默认监视配置，会自动热重载；需提前安排维护窗口。')


def main():
    parser = argparse.ArgumentParser(description='V2bX-2 安全配置向导/编辑器（标准 JSON）')
    parser.add_argument('--check', metavar='FILE')
    parser.add_argument('--config', default='/etc/V2bX/config.json')
    parser.add_argument('--root', help='隔离验收根；不调用任何宿主服务')
    parser.add_argument('--offline', action='store_true', help='仅离线生成；与普通离线向导相同')
    parser.add_argument('--edit', metavar='EDITOR')
    args = parser.parse_args()
    root = Path(args.root).resolve(strict=True) if args.root else None
    if root == Path('/'):
        raise ValueError('--root 不能是宿主根')
    if args.check:
        path = safe_path(Path(args.check), root)
        count = validate(read_document(path))
        print(f'标准 JSON/基础字段校验通过（{count} 节点）。不代表证书/核心/面板连通性验证；未输出密钥。')
        return 0
    if root is None and os.geteuid() != 0:
        raise ValueError('写入宿主配置需要 root；隔离验收请指定 --root 和 --config')
    path = safe_path(Path(args.config), root)
    expected = path.read_bytes() if path.exists() else None
    if expected is not None:
        print('警告：此操作会替换整个配置。运行中的核心默认监视文件，保存可能触发热重载。')
        if ask('请输入 REPLACE 继续，其他输入取消', '') != 'REPLACE':
            print('已取消，原配置未修改。')
            return 0
    if args.edit:
        if expected is None:
            raise ValueError('原配置不存在，请先运行 v2bx generate')
        with tempfile.TemporaryDirectory(prefix='v2bx-config-edit-') as temp:
            draft = Path(temp) / 'config.json'
            draft.write_bytes(expected)
            draft.chmod(0o600)
            subprocess.run(shlex.split(args.edit) + [str(draft)], check=True)
            document = read_document(draft)
            validate(document)
    else:
        document = generate_document()
    if ask('确认写入配置？输入 SAVE，其他输入取消', '') != 'SAVE':
        print('已取消，没有写入配置。')
        return 0
    write_atomic(path, document, expected)
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (EOFError, KeyboardInterrupt):
        print('\n输入中断，未继续写入配置。', file=sys.stderr)
        sys.exit(1)
    except json.JSONDecodeError:
        print('JSON 格式错误，未输出配置内容。', file=sys.stderr)
        sys.exit(1)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('操作失败：' + str(error), file=sys.stderr)
        sys.exit(1)
