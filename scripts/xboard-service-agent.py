#!/usr/bin/env python3
"""Xboard machine service synchronizer. Uses only Python's standard library."""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def read(path, default=None):
    try:
        return json.loads(Path(path).read_text(encoding="utf-8-sig"))
    except FileNotFoundError:
        return default


def write(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_name(path.name + ".tmp")
    fd = os.open(temp, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(data, stream, ensure_ascii=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)


class Agent:
    def __init__(self, config):
        self.config = config
        endpoint = urllib.parse.urlsplit(config["panel"])
        if endpoint.scheme != "https" and not (endpoint.scheme == "http" and endpoint.hostname in {"127.0.0.1", "localhost", "::1"}):
            raise ValueError("面板必须使用 HTTPS；HTTP 仅用于本机联调")
        self.base = Path(config["state_dir"]).resolve()
        self.base.mkdir(parents=True, exist_ok=True)
        self.state_path = self.base / "agent-state.json"
        self.state = read(self.state_path, {"services": {}, "pending": []})
        self.cli = [config["v2bx"], "services", "-c", config["v2bx_config"]]

    def request(self, action, payload):
        data = {"machine_id": self.config["machine_id"], "token": self.config["token"], **payload}
        # Prefer the original machine resource. Keep the old endpoint only
        # as a compatibility path for already-deployed panel adaptations.
        paths = ["/api/v2/server/machine/services/", "/api/v2/server/machine/v2bx/"]
        for index, path in enumerate(paths):
            req = urllib.request.Request(self.config["panel"].rstrip("/") + path + action,
                                         json.dumps(data).encode(), {"Content-Type": "application/json", "Accept": "application/json"})
            try:
                with urllib.request.urlopen(req, timeout=20) as response:
                    return json.load(response)
            except urllib.error.HTTPError as error:
                status = error.code
                error.close()
                if status == 404 and index == 0:
                    continue
                raise RuntimeError("面板请求失败 HTTP " + str(status)) from None

    def run(self, *args):
        result = subprocess.run(self.cli + list(args), capture_output=True, encoding="utf-8", timeout=30)
        if result.returncode:
            # Backend output can contain local paths or credentials: do not forward it.
            raise RuntimeError("V2bX 服务命令失败，退出码 " + str(result.returncode))
        return json.loads(result.stdout) if result.stdout.strip() else None

    def persist(self):
        write(self.state_path, self.state)

    def flush(self):
        while self.state["pending"]:
            batch = self.state["pending"][0]
            result = self.request("meter", batch)
            if result.get("ack") != batch["batch_id"]:
                raise RuntimeError("面板计量确认不匹配")
            self.state["pending"].pop(0)
            self.persist()

    def key(self, sid, entry):
        path = self.base / (sid + ".key")
        if not path.exists():
            generated = self.run("keygen", "-o", str(path))
            entry["server_public_key"] = generated["public_key"]
            self.persist()
        if not entry.get("server_public_key"):
            raise RuntimeError("私钥文件缺少对应公钥状态，请先恢复代理状态备份")
        return str(path)

    def resolve(self, row, entry):
        spec = copy.deepcopy(row["spec"])
        sid = spec["ID"]
        if sid != "xb-" + str(row["id"]):
            raise ValueError("服务 ID 不属于面板命名空间")
        if "WireGuard" in spec:
            spec["WireGuard"]["PrivateKeyFile"] = self.key(sid, entry)
        if spec["Type"] in {"amneziawg", "mtproto"}:
            name = spec.pop("BackendProfile")
            profile = copy.deepcopy(self.config.get("backend_profiles", {}).get(name))
            if not profile:
                raise RuntimeError("节点未配置请求的后端 Profile")
            expected = "mtg-2.1.7" if spec["Type"] == "mtproto" else "amneziawg-go-v3.1.20260828"
            if profile.get("Version") != expected:
                raise RuntimeError("后端 Profile 版本不匹配")
            binary = Path(profile["Executable"])
            if not binary.is_absolute() or hashlib.sha256(binary.read_bytes()).hexdigest() != profile["SHA256"].lower():
                raise RuntimeError("后端二进制哈希不匹配")
            if spec["Type"] == "amneziawg":
                profile["Interface"] = "xb" + str(row["id"])
                profile["ControlSocket"] = "/var/run/amneziawg/" + profile["Interface"] + ".sock"
                profile["Obfuscation"] = spec.pop("Obfuscation", {})
            else:
                secret = spec.pop("Secret")
                secret_path = self.base / (sid + ".secret")
                fd = os.open(secret_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
                with os.fdopen(fd, "w", encoding="utf-8") as stream:
                    stream.write(secret + "\n")
                profile["SecretFile"] = str(secret_path)
                profile["PublicIP"] = spec.pop("PublicIP")
                profile["Concurrency"] = spec.pop("Concurrency", 8192)
                # Unique per-service loopback port, explicitly reserved in node configuration.
                ports = self.config.get("metrics_ports", {})
                if sid not in ports:
                    raise RuntimeError("节点未为 MTProto 服务预留指标端口")
                profile["MetricsListen"] = "127.0.0.1:" + str(int(ports[sid]))
            spec["Backend"] = profile
        return spec

    def status(self, sid):
        rows = self.run("list", "--id", sid)
        return rows[0].get("状态") if rows else None

    def meter(self, row, entry, status):
        current = {str(uid): [int(v["Upload"]), int(v["Download"])]
                   for uid, v in status.get("Traffic", {}).items()}
        previous = entry.get("cursor", {})
        delta = {}
        for uid, pair in current.items():
            old = previous.get(uid, [0, 0])
            if any(pair[i] < old[i] for i in range(2)):
                raise RuntimeError("节点累计计数器回退，需要恢复计量状态后继续")
            diff = [pair[i] - old[i] for i in range(2)]
            if any(diff):
                delta[uid] = diff
        if delta:
            self.state["pending"].append({"service_id": row["id"], "batch_id": uuid.uuid4().hex, "traffic": delta})
        entry["cursor"] = current
        self.persist()
        self.flush()

    def sync(self):
        self.flush()
        result = self.request("pull", {})
        if result.get("contract") != 1 or not isinstance(result.get("services"), list):
            raise RuntimeError("服务同步契约不匹配")
        for row in result["services"]:
            sid = "xb-" + str(row["id"])
            entry = self.state["services"].setdefault(sid, {})
            if row["deleting"] and entry.get("removal_ready") == row["hash"] and not self.run("list", "--id", sid):
                self.request("ack", {"service_id": row["id"], "revision": row["revision"], "hash": row["hash"],
                                     "state": "removed", "runtime": entry.get("last_runtime", {})})
                continue
            runtime = {}
            try:
                resolved = self.resolve(row, entry)
                fingerprint = hashlib.sha256(json.dumps(resolved, sort_keys=True).encode()).hexdigest()
                if entry.get("hash") != fingerprint:
                    resolved["Revision"] = time.time_ns() // 1000
                    path = self.base / (sid + ".json")
                    write(path, resolved)
                    self.run("put", "-f", str(path))
                    entry.update(hash=fingerprint, applied=resolved["Revision"])
                    self.persist()
                status = None
                for attempt in range(10):
                    status = self.status(sid)
                    if status and status.get("AppliedRevision") == entry["applied"]:
                        break
                    time.sleep(0.3)
                if not status or status.get("AppliedRevision") != entry["applied"] or status.get("State") not in {"运行中", "已停用"}:
                    raise RuntimeError("节点尚未成功应用该配置，请检查节点本机日志和权限")
                self.meter(row, entry, status)
                runtime = {"peers": status.get("Peers", []), "process": status.get("Process"),
                           "server_public_key": entry.get("server_public_key")}
                if resolved.get("Backend"):
                    runtime.update(backend_version=resolved["Backend"]["Version"], backend_sha256=resolved["Backend"]["SHA256"])
                state = "running" if row["spec"]["Enabled"] else "disabled"
                if row["deleting"]:
                    entry["removal_ready"] = row["hash"]
                    entry["last_runtime"] = runtime
                    self.persist()
                    self.run("remove", "--id", sid)
                    state = "removed"
            except Exception as error:
                state = "error"
                runtime = {"error": str(error) if isinstance(error, (RuntimeError, ValueError)) else "本机服务同步失败"}
            self.request("ack", {"service_id": row["id"], "revision": row["revision"], "hash": row["hash"],
                                 "state": state, "runtime": runtime})
        print(json.dumps({"services": len(result["services"]), "pending_batches": len(self.state["pending"])}, ensure_ascii=False), flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    agent = Agent(read(args.config))
    lock = open(agent.base / "agent.lock", "a+b")
    if os.name == "nt":
        import msvcrt
        lock.seek(0)
        lock.write(b"0")
        lock.flush()
        lock.seek(0)
        msvcrt.locking(lock.fileno(), msvcrt.LK_NBLCK, 1)
    else:
        import fcntl
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    while True:
        try:
            agent.sync()
        except Exception:
            if args.once:
                raise SystemExit("同步失败，请检查节点本机记录和面板状态") from None
            print("同步失败，保留配置与待报计量后重试", flush=True)
        if args.once:
            break
        time.sleep(max(2, int(agent.config.get("interval", 10))))


if __name__ == "__main__":
    main()
