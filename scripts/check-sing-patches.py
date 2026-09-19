#!/usr/bin/env python3
"""Verify the embedded sing-box source against the checksummed official module.

Use --write only after reviewing a deliberate compatibility patch or rebase.
No source is modified by the default check.
"""
import hashlib
import json
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
EMBEDDED = ROOT / "third_party" / "sing-box"
MANIFEST = ROOT / "third_party" / "sing-box-patches.json"
VERSION = "v1.14.1"
OMIT = {".github", "docs", "release", "test", ".gitignore", ".goreleaser.yaml", "mkdocs.yml"}


def snapshot(root, omit=()):
    result = {}
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(root)
        if rel.parts[0] in omit:
            continue
        result[rel.as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def main():
    info = json.loads(subprocess.check_output(
        ["go", "mod", "download", "-json", "github.com/sagernet/sing-box@" + VERSION],
        cwd=ROOT, text=True))
    if "Error" in info:
        raise RuntimeError(info["Error"])
    upstream = snapshot(pathlib.Path(info["Dir"]), OMIT)
    embedded = snapshot(EMBEDDED)
    changes = {}
    for path in sorted(upstream.keys() | embedded.keys()):
        if upstream.get(path) != embedded.get(path):
            changes[path] = {"upstream_sha256": upstream.get(path), "patched_sha256": embedded.get(path)}
    result = {"module": "github.com/sagernet/sing-box", "version": VERSION,
              "module_sum": info["Sum"], "upstream_commit": info["Origin"]["Hash"],
              "omitted_top_level": sorted(OMIT), "patches": changes}
    if sys.argv[1:] == ["--write"]:
        MANIFEST.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
        print(f"Recorded {len(changes)} compatibility source changes; review the manifest before committing.")
    elif sys.argv[1:]:
        raise SystemExit("Usage: check-sing-patches.py [--write]")
    elif result != json.loads(MANIFEST.read_text(encoding="utf-8")):
        raise SystemExit("Embedded sing-box differs from its reviewed patch manifest. Do not release.")
    else:
        print(f"Verified official {VERSION} + {len(changes)} reviewed compatibility source changes.")


if __name__ == "__main__":
    main()
