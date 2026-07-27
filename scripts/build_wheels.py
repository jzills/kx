"""Package the cross-compiled kx binaries as PyPI wheels.

The wheels carry no Python code: each holds one prebuilt binary under
`<name>-<version>.data/scripts/`, which pip installs straight into the
environment's bin/ directory. There is no console-script shim, so `kx` on PATH
is the binary itself and pays no interpreter startup.

Wheels are assembled directly rather than through setuptools. The contents are
a single executable and three metadata files, so the build backend would add
machinery without adding correctness — and the platform tag, which is the part
that actually matters here, is set explicitly either way.

    python scripts/build_wheels.py --version 0.1.0 --binaries dist/binaries

`--binaries` is the directory the release workflow cross-compiles into, holding
`kx_<os>_<arch>/kx` for each target.

A wheel built for a platform kx doesn't publish is not an error worth failing
the release over, but a *missing* one is: every tag listed below must be built,
or a user on that platform silently gets "no matching distribution".
"""

from __future__ import annotations

import argparse
import base64
import csv
import hashlib
import io
import stat
import sys
import tomllib
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# Go target → wheel platform tag.
#
# The manylinux tags are nominal: a CGO_ENABLED=0 binary links against no libc
# at all, so it satisfies any glibc version. pip still needs the tag to resolve
# the wheel, and musllinux is what makes `pip install kx-cli` work on Alpine —
# the case that started this port.
TARGETS = {
    "linux_amd64": ["manylinux_2_17_x86_64", "musllinux_1_2_x86_64"],
    "linux_arm64": ["manylinux_2_17_aarch64", "musllinux_1_2_aarch64"],
    "darwin_amd64": ["macosx_10_9_x86_64"],
    "darwin_arm64": ["macosx_11_0_arm64"],
}

DIST_NAME = "kx_cli"


def _metadata(version: str) -> str:
    project = tomllib.loads((ROOT / "pyproject.toml").read_text())["project"]
    readme = (ROOT / project["readme"]).read_text()
    return (
        "Metadata-Version: 2.1\n"
        f"Name: {project['name']}\n"
        f"Version: {version}\n"
        f"Summary: {project['description']}\n"
        f"Requires-Python: {project['requires-python']}\n"
        "Description-Content-Type: text/markdown\n"
        "\n"
        f"{readme}"
    )


def _wheel_metadata(tag: str) -> str:
    return (
        "Wheel-Version: 1.0\n"
        "Generator: kx build_wheels.py\n"
        "Root-Is-Purelib: false\n"
        f"Tag: {tag}\n"
    )


def _record_hash(data: bytes) -> str:
    digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=")
    return f"sha256={digest.decode()}"


def build_wheel(version: str, binary: Path, tag: str, outdir: Path) -> Path:
    full_tag = f"py3-none-{tag}"
    name = f"{DIST_NAME}-{version}"
    wheel_path = outdir / f"{name}-{full_tag}.whl"

    binary_bytes = binary.read_bytes()
    entries = {
        f"{name}.data/scripts/kx": binary_bytes,
        f"{name}.dist-info/METADATA": _metadata(version).encode(),
        f"{name}.dist-info/WHEEL": _wheel_metadata(full_tag).encode(),
    }

    record = io.StringIO()
    writer = csv.writer(record, lineterminator="\n")
    for path, data in entries.items():
        writer.writerow([path, _record_hash(data), len(data)])
    writer.writerow([f"{name}.dist-info/RECORD", "", ""])
    entries[f"{name}.dist-info/RECORD"] = record.getvalue().encode()

    outdir.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(wheel_path, "w", zipfile.ZIP_DEFLATED) as archive:
        for path, data in entries.items():
            info = zipfile.ZipInfo(path)
            # The regular-file bits are not decoration: pip decides whether to
            # keep the executable bit with stat.S_ISREG(mode), which is false
            # for a bare 0o755, so the installed binary lands non-executable
            # and `kx` fails with "permission denied".
            mode = 0o755 if path.endswith("/kx") else 0o644
            info.external_attr = (stat.S_IFREG | mode) << 16
            archive.writestr(info, data)
    return wheel_path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--binaries", required=True, type=Path)
    parser.add_argument("--outdir", type=Path, default=ROOT / "dist" / "wheels")
    args = parser.parse_args()

    built = []
    for target, tags in TARGETS.items():
        binary = args.binaries / f"kx_{target}" / "kx"
        if not binary.exists():
            print(f"missing binary for {target}: {binary}", file=sys.stderr)
            return 1
        for tag in tags:
            path = build_wheel(args.version, binary, tag, args.outdir)
            built.append(path)
            print(f"built {path.name}")

    print(f"\n{len(built)} wheels in {args.outdir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
