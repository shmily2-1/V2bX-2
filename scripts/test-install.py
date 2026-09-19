#!/usr/bin/env python3
"""Offline installer regression tests; all writes stay inside temporary directories.

Only downloads, platform detection and service inspection are mocked. ZIP extraction,
SHA256 verification, backups, symlinks and atomic file replacement use real tools.
No package manager, service startup, firewall or external endpoint is contacted.
"""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile

INSTALLER = Path(__file__).with_name("install.sh").resolve()
BINARY = b'#!/usr/bin/env bash\n[[ ${1:-} == version ]] || exit 99\necho "fixture V2bX"\n'
SAMPLE = b'{"Nodes": [], "example": true}\n'


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="v2bx-install-test-")
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.root = self.base / "root"
        self.mock = self.base / "mock"
        self.assets = self.base / "assets"
        self.mock.mkdir()
        self.assets.mkdir()
        self.log = self.base / "commands.jsonl"
        self.env = os.environ | {
            "PATH": str(self.mock) + os.pathsep + os.environ["PATH"],
            "FIXTURE_ASSETS": str(self.assets), "FIXTURE_LOG": str(self.log),
            "FIXTURE_ARCH": "x86_64", "FIXTURE_OS": "Linux",
            "FIXTURE_SERVICE": "", "FIXTURE_DOWNLOAD_FAIL": "",
        }
        self.write_mock("curl", '''
import os, pathlib, shutil, sys, json
args = sys.argv[1:]
url = next(a for a in args if a.startswith("https://"))
with open(os.environ["FIXTURE_LOG"], "a") as f: f.write(json.dumps(["curl", url]) + "\\n")
assert url.startswith("https://github.com/shmily2-1/V2bX-2/releases/download/v")
assert "--proto-redir" in args and "=https" in args
if os.environ["FIXTURE_DOWNLOAD_FAIL"]: sys.exit(22)
shutil.copyfile(pathlib.Path(os.environ["FIXTURE_ASSETS"]) / url.rsplit("/", 1)[1], args[args.index("-o") + 1])
''')
        self.write_mock("uname", '''
import os, sys
print(os.environ["FIXTURE_OS" if sys.argv[1] == "-s" else "FIXTURE_ARCH"])
''')
        self.write_mock("id", 'print("0")\n')
        self.write_mock("systemctl", '''
import os, sys, json
with open(os.environ["FIXTURE_LOG"], "a") as f: f.write(json.dumps(["systemctl"] + sys.argv[1:]) + "\\n")
assert sys.argv[1] == "show", "Tests must never control a host service"
service = os.environ["FIXTURE_SERVICE"]
if "--property=LoadState" in sys.argv: print("loaded" if service else "not-found")
else: print("{ path=" + service + " ; argv[]=" + service + " server ; }")
''')
        for manager in ("apt-get", "dnf", "yum"):
            self.write_mock(manager, 'raise RuntimeError("Tests must never install host packages")\n')
        self.make_archive()

    def write_mock(self, name, body):
        path = self.mock / name
        path.write_text("#!/usr/bin/env python3\n" + body)
        path.chmod(0o755)

    def make_archive(self, arch="amd64", binary=BINARY, sample=SAMPLE):
        path = self.assets / f"V2bX-linux-{arch}.zip"
        with zipfile.ZipFile(path, "w") as z:
            if binary is not None:
                z.writestr("V2bX", binary)
            if sample is not None:
                z.writestr("example/hysteria2-xboard.json", sample)
            z.writestr("../../MUST_NOT_EXTRACT", "unsafe extra member")
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        Path(str(path) + ".sha256").write_text(f"{digest}  {path.name}\n")

    def file(self, name, contents):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
        return path

    def run_installer(self, *args, ok=True, host_inspection=False):
        # The only non-staged tests use a mock service whose binary is under self.base.
        # They do not request systemd files or dependency installation.
        if host_inspection:
            assert "--with-systemd" not in args and "--install-deps" not in args
            assert self.env["FIXTURE_SERVICE"].startswith(str(self.base) + "/")
            prefix = []
        else:
            prefix = ["--destdir", str(self.root)]
        result = subprocess.run(["bash", str(INSTALLER), *prefix, *args], env=self.env,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=15)
        self.assertEqual(result.returncode == 0, ok, result.stdout)
        self.assertFalse(list(self.root.glob("**/.V2bX.new.*")), "Leaked temporary binary")
        return result.stdout

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_fresh_binary_only(self):
        self.run_installer()
        binary = self.root / "usr/local/bin/V2bX"
        self.assertEqual(binary.read_bytes(), BINARY)
        self.assertEqual(binary.stat().st_mode & 0o777, 0o755)
        self.assertFalse((self.root / "etc").exists())
        self.assertTrue(all(c[0] == "curl" for c in self.calls()))
        self.assertIn("/v0.1.0-core-upgrade.1/", self.calls()[0][1])
        self.assertFalse((self.root / "MUST_NOT_EXTRACT").exists())

    def test_systemd_staging(self):
        self.run_installer("--with-systemd")
        unit = (self.root / "etc/systemd/system/V2bX.service").read_text()
        self.assertIn("ExecStart=/usr/local/bin/V2bX server --config /etc/V2bX/config.json", unit)
        example = self.root / "etc/V2bX/config.json.example"
        self.assertEqual(example.read_bytes(), SAMPLE)
        self.assertEqual(example.stat().st_mode & 0o777, 0o600)
        self.assertFalse((self.root / "etc/V2bX/config.json").exists())
        self.assertFalse(any(c[0] == "systemctl" for c in self.calls()))

    def test_update_preserves_configuration_and_service(self):
        old = self.file("usr/local/bin/V2bX", "old-binary")
        config = self.file("etc/V2bX/config.json", "secret panel config")
        example = self.file("etc/V2bX/config.json.example", "local example")
        unit = self.file("etc/systemd/system/V2bX.service", "custom unit")
        self.run_installer("--with-systemd")
        self.assertEqual(old.read_bytes(), BINARY)
        self.assertEqual([p.read_text() for p in old.parent.glob("V2bX.backup.*")], ["old-binary"])
        self.assertEqual(config.read_text(), "secret panel config")
        self.assertEqual(example.read_text(), "local example")
        self.assertEqual(unit.read_text(), "custom unit")

    def test_repeated_updates_keep_distinct_backups(self):
        self.run_installer()
        self.run_installer()
        self.run_installer()
        self.assertEqual(len(list((self.root / "usr/local/bin").glob("V2bX.backup.*"))), 2)

    def test_legacy_path(self):
        old = self.file("usr/local/V2bX/V2bX", "legacy")
        self.run_installer()
        self.assertEqual(old.read_bytes(), BINARY)
        self.assertFalse((self.root / "usr/local/bin/V2bX").exists())

    def test_legacy_symlink_is_preserved(self):
        old = self.file("usr/local/V2bX/V2bX", "legacy")
        link = self.root / "usr/local/bin/V2bX"
        link.parent.mkdir()
        link.symlink_to("../V2bX/V2bX")
        self.run_installer()
        self.assertTrue(link.is_symlink())
        self.assertEqual(old.read_bytes(), BINARY)

    def test_ambiguous_path_requires_explicit_selection(self):
        old = self.file("usr/local/V2bX/V2bX", "legacy")
        modern = self.file("usr/local/bin/V2bX", "modern")
        self.run_installer(ok=False)
        self.assertEqual(modern.read_text(), "modern")
        self.assertFalse(self.calls())
        self.run_installer("--install-dir", "/usr/local/V2bX")
        self.assertEqual(old.read_bytes(), BINARY)
        self.assertEqual(modern.read_text(), "modern")

    def test_custom_dir_and_explicit_version(self):
        self.run_installer("v9.8.7-test.1", "--install-dir", "/opt/v2bx", "--with-systemd")
        self.assertEqual((self.root / "opt/v2bx/V2bX").read_bytes(), BINARY)
        self.assertIn("/v9.8.7-test.1/", self.calls()[0][1])
        self.assertIn("ExecStart=/opt/v2bx/V2bX ", (self.root / "etc/systemd/system/V2bX.service").read_text())

    def test_arm64_asset_selection(self):
        self.env["FIXTURE_ARCH"] = "aarch64"
        self.make_archive(arch="arm64")
        self.run_installer()
        self.assertTrue(self.calls()[0][1].endswith("V2bX-linux-arm64.zip"))

    def test_checksum_failure_preserves_existing_binary(self):
        old = self.file("usr/local/bin/V2bX", "old")
        (self.assets / "V2bX-linux-amd64.zip.sha256").write_text("0" * 64 + "  V2bX-linux-amd64.zip\n")
        self.run_installer(ok=False)
        self.assertEqual(old.read_text(), "old")
        self.assertFalse(list(old.parent.glob("V2bX.backup.*")))

    def test_malformed_checksums(self):
        for text in ("", "x", "0" * 64 + "  /etc/shadow\n", "0" * 64 + "  V2bX-linux-amd64.zip\nextra\n"):
            with self.subTest(text=text):
                (self.assets / "V2bX-linux-amd64.zip.sha256").write_text(text)
                self.run_installer(ok=False)
                self.assertFalse((self.root / "usr/local/bin/V2bX").exists())

    def test_download_failure(self):
        self.env["FIXTURE_DOWNLOAD_FAIL"] = "yes"
        old = self.file("usr/local/bin/V2bX", "old")
        self.run_installer(ok=False)
        self.assertEqual(old.read_text(), "old")

    def test_missing_archive_binary(self):
        self.make_archive(binary=None)
        self.run_installer(ok=False)
        self.assertFalse((self.root / "usr").exists())

    def test_broken_binary(self):
        self.make_archive(binary=b"#!/usr/bin/env bash\nexit 42\n")
        self.run_installer(ok=False)
        self.assertFalse((self.root / "usr").exists())

    def test_missing_config_example_fails_before_update(self):
        self.make_archive(sample=None)
        old = self.file("usr/local/bin/V2bX", "old")
        self.run_installer("--with-systemd", ok=False)
        self.assertEqual(old.read_text(), "old")

    def test_invalid_args_fail_without_download(self):
        for args in (("v1/../../x",), ("v1", "v2"), ("--bogus",), ("--install-dir",),
                     ("--install-dir", "/bad path"), ("--destdir", "/"), ("--install-deps",)):
            with self.subTest(args=args):
                self.run_installer(*args, ok=False)
        self.assertFalse(self.calls())

    def test_unsupported_platforms(self):
        self.env["FIXTURE_ARCH"] = "i386"
        self.run_installer(ok=False)
        self.env["FIXTURE_ARCH"] = "x86_64"
        self.env["FIXTURE_OS"] = "Darwin"
        self.run_installer(ok=False)
        self.assertFalse(self.calls())

    def test_destdir_symlink_cannot_escape(self):
        self.root.mkdir()
        outside = self.base / "outside"
        outside.mkdir()
        (self.root / "usr").symlink_to(outside, target_is_directory=True)
        self.run_installer(ok=False)
        self.assertEqual(list(outside.iterdir()), [])

    def test_masked_service_is_preserved(self):
        unit = self.root / "etc/systemd/system/V2bX.service"
        unit.parent.mkdir(parents=True)
        unit.symlink_to("/dev/null")
        self.run_installer("--with-systemd")
        self.assertTrue(unit.is_symlink())
        self.assertEqual(os.readlink(unit), "/dev/null")

    def test_vendor_unit_is_preserved(self):
        unit = self.file("usr/lib/systemd/system/V2bX.service", "vendor unit")
        self.run_installer("--with-systemd")
        self.assertEqual(unit.read_text(), "vendor unit")
        self.assertFalse((self.root / "etc/systemd/system/V2bX.service").exists())

    def test_install_lock(self):
        old = self.file("usr/local/bin/V2bX", "old")
        with (old.parent / ".V2bX.install.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.run_installer(ok=False)
        self.assertEqual(old.read_text(), "old")

    def test_detects_service_execstart(self):
        target = self.file("opt/custom/V2bX", "old")
        self.env["FIXTURE_SERVICE"] = str(target)
        self.run_installer(host_inspection=True)
        self.assertEqual(target.read_bytes(), BINARY)
        self.assertTrue(any(c[0] == "systemctl" for c in self.calls()))

    def test_refuses_mismatched_service_target(self):
        target = self.file("opt/custom/V2bX", "old")
        self.env["FIXTURE_SERVICE"] = str(target)
        self.run_installer("--install-dir", str(self.base / "wrong"), host_inspection=True, ok=False)
        self.assertEqual(target.read_text(), "old")

    def test_refuses_symlink_to_non_binary_file(self):
        sensitive = self.file("etc/secret", "must remain")
        target = self.root / "opt/custom/V2bX"
        target.parent.mkdir(parents=True)
        target.symlink_to(sensitive)
        self.env["FIXTURE_SERVICE"] = str(target)
        self.run_installer(host_inspection=True, ok=False)
        self.assertEqual(sensitive.read_text(), "must remain")
        self.assertFalse(any(c[0] == "curl" for c in self.calls()))

    def test_custom_symlink_requires_actual_directory(self):
        real = self.file("opt/actual/V2bX", "old")
        target = self.root / "opt/custom/V2bX"
        target.parent.mkdir(parents=True)
        target.symlink_to(real)
        self.env["FIXTURE_SERVICE"] = str(target)
        self.run_installer(host_inspection=True, ok=False)
        self.run_installer("--install-dir", str(real.parent), host_inspection=True)
        self.assertTrue(target.is_symlink())
        self.assertEqual(real.read_bytes(), BINARY)

    def test_unit_write_failure_preserves_old_binary(self):
        old = self.file("usr/local/bin/V2bX", "old")
        # systemd/system is a file, so the later unit directory creation fails.
        self.file("etc/systemd/system", "not a directory")
        self.run_installer("--with-systemd", ok=False)
        self.assertEqual(old.read_text(), "old")


if __name__ == "__main__":
    for tool in ("bash", "unzip", "sha256sum", "flock"):
        if not shutil.which(tool):
            raise SystemExit(f"Required for testing: {tool}")
    unittest.main(verbosity=2)
