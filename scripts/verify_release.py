"""Verify the six published archives and execute the native binary's version check."""

import argparse
import hashlib
import json
import platform
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile


def verify(directory: Path, version: str) -> None:
    checksums = {}
    for line in (directory / "checksums.txt").read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        name = name.lstrip("*")
        if Path(name).name != name or name in checksums:
            raise ValueError(f"Invalid or duplicate checksum filename: {name}")
        checksums[name] = digest

    host_os = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}[platform.system()]
    host_arch = {"amd64": "amd64", "x86_64": "amd64", "arm64": "arm64", "aarch64": "arm64"}[platform.machine().lower()]
    for goos in ("linux", "darwin", "windows"):
        binary = "paperless.exe" if goos == "windows" else "paperless"
        extension = "zip" if goos == "windows" else "tar.gz"
        for arch in ("amd64", "arm64"):
            name = f"paperless_{version}_{goos}_{arch}.{extension}"
            archive = directory / name
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            if checksums.get(name) != digest:
                raise ValueError(f"Checksum mismatch or missing checksum: {name}")
            if extension == "zip":
                with zipfile.ZipFile(archive) as bundle:
                    payload = bundle.read(binary)
            else:
                with tarfile.open(archive, "r:gz") as bundle:
                    member = bundle.getmember(binary)
                    if not member.isfile():
                        raise ValueError(f"Expected a regular binary in {name}")
                    with bundle.extractfile(member) as stream:
                        payload = stream.read()
            if not payload:
                raise ValueError(f"Empty binary in {name}")
            if (goos, arch) == (host_os, host_arch):
                with tempfile.TemporaryDirectory(prefix="paperless-release-check-") as temp:
                    executable = Path(temp) / binary
                    executable.write_bytes(payload)
                    executable.chmod(0o700)
                    result = subprocess.run([str(executable), "--version"], check=True, capture_output=True, text=True, timeout=30)
                    if result.stdout.strip() != f"paperless version {version}":
                        raise ValueError(f"Unexpected native version: {result.stdout!r}")
            print(f"Verified {name}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--version", help="Version without v; defaults to GoReleaser metadata.json")
    args = parser.parse_args()
    version = args.version or json.loads((args.directory / "metadata.json").read_text())["version"]
    verify(args.directory, version)
