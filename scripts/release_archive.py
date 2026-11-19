"""Build and verify the installable source attachments for a release commit."""

import argparse
import hashlib
import re
import stat
import subprocess
import tarfile
import zipfile
from pathlib import Path, PurePosixPath

PUBLIC_DATA = {
    "data/.env.example",
    "data/config.example.json",
    "data/poll_prompt.txt",
    "data/prompt.txt",
    "data/qa_persona.txt",
}
REQUIRED = PUBLIC_DATA | {
    ".dockerignore",
    "Dockerfile",
    "docker-compose.yml",
    "docker-entrypoint.sh",
    "VERSION",
    "go.mod",
    "go.sum",
    "cmd/sakura-main/main.go",
    "cmd/sakura-qa/main.go",
    "internal/db/guard/schema.sql",
    "internal/guard/opencc/LICENSE",
    "internal/guard/opencc/NOTICE",
    "internal/tools/tool_schemas.json",
    "web/package.json",
    "web/package-lock.json",
    "web/index.html",
    "web/src/main.ts",
    "scripts/deploy.sh",
    "scripts/smoke_image.sh",
    "deploy/install-deployer.sh",
    "docs/release-archives.md",
}
REVISION_FILE = "RELEASE_REVISION"


def git(*args):
    return subprocess.check_output(["git", *args]).decode("utf-8").strip()


def source_paths(revision):
    # Ship all tracked sources and resources. Ship only known templates under data/.
    roots = set(git("ls-tree", "--name-only", revision).splitlines())
    paths = sorted((roots - {".github", ".sakura", "data"}) | PUBLIC_DATA)
    entries = git("ls-tree", "-r", "-z", revision, "--", *paths).rstrip("\0").split("\0")
    files = set()
    for entry in entries:
        header, name = entry.split("\t", 1)
        path = PurePosixPath(name)
        if (
            header.split()[0] not in {"100644", "100755"}
            or REVISION_FILE in path.parts
            or set(path.parts)
            & {".git", "node_modules", "dist", "bin", "sessions", "logs", "__pycache__"}
            or (
                name not in PUBLIC_DATA
                and (
                    path.name.startswith((".env", "bot_session", "user_session"))
                    or path.suffix
                    in {".db", ".sqlite", ".sqlite3", ".session", ".pem", ".key", ".exe"}
                )
            )
        ):
            raise SystemExit(f"Unexpected tracked path in release sources: {name}")
        files.add(name)
    if missing := REQUIRED - files:
        raise SystemExit(f"Missing release inputs: {sorted(missing)}")
    return paths, files


def digest(path):
    with path.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def checksums(archives):
    return "".join(f"{digest(archive)}  {archive.name}\n" for archive in archives)


def build(revision, paths, archives, checksum_file, prefix):
    checksum_file.parent.mkdir(parents=True, exist_ok=True)
    for archive, kind in zip(archives, ("zip", "tar.gz"), strict=True):
        subprocess.run(
            [
                "git",
                "-c",
                "tar.umask=0022",
                "archive",
                f"--format={kind}",
                f"--prefix={prefix}/",
                f"--add-virtual-file={prefix}/{REVISION_FILE}:{revision}\n",
                f"--output={archive}",
                revision,
                "--",
                *paths,
            ],
            check=True,
        )
    checksum_file.write_text(checksums(archives), encoding="ascii", newline="\n")


def verify(archives, checksum_file, prefix, expected, revision, extract_to):
    if checksum_file.read_text(encoding="ascii") != checksums(archives):
        raise SystemExit("Release attachment SHA-256 mismatch")
    expected_files = {f"{prefix}/{name}" for name in expected | {REVISION_FILE}}
    expected_dirs = {str(p) for name in expected_files for p in PurePosixPath(name).parents}
    revision_digest = hashlib.sha256(f"{revision}\n".encode()).hexdigest()
    manifests = []
    for archive in archives:
        is_zip = archive.suffix == ".zip"
        container = zipfile.ZipFile(archive) if is_zip else tarfile.open(archive, "r:gz")
        with container:
            members = container.infolist() if is_zip else container.getmembers()
            files = {}
            for member in members:
                name = member.filename if is_zip else member.name
                is_directory = member.is_dir() if is_zip else member.isdir()
                if is_directory:
                    if name.rstrip("/") not in expected_dirs:
                        raise SystemExit(f"Unexpected directory in {archive.name}: {name}")
                    continue
                # Git for Windows can omit POSIX type bits in its ZIP entries.
                if is_zip:
                    is_file = stat.S_IFMT(member.external_attr >> 16) in {0, stat.S_IFREG}
                else:
                    is_file = member.isfile()
                if name not in expected_files or name in files or not is_file:
                    raise SystemExit(f"Unexpected file in {archive.name}: {name}")
                handle = container.open(member) if is_zip else container.extractfile(member)
                with handle:
                    files[name] = hashlib.file_digest(handle, "sha256").hexdigest()
            if missing := expected_files - files.keys():
                raise SystemExit(f"Missing files in {archive.name}: {sorted(missing)}")
            if files[f"{prefix}/{REVISION_FILE}"] != revision_digest:
                raise SystemExit(f"Incorrect release revision in {archive.name}")
            if extract_to is not None:
                target = extract_to / ("zip" if is_zip else "tar")
                target.mkdir(parents=True)
                if is_zip:
                    container.extractall(target)
                else:
                    container.extractall(target, filter="data")
            manifests.append(files)
    if manifests[0] != manifests[1]:
        raise SystemExit("ZIP and tar.gz contain different file contents")
    print(f"Verified SHA-256 and {len(expected_files)} files in each attachment ({revision})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("build", "verify"))
    parser.add_argument("--revision", default="HEAD")
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--extract-to", type=Path)
    args = parser.parse_args()
    revision = git("rev-parse", "--verify", f"{args.revision}^{{commit}}")
    version = git("show", f"{revision}:VERSION")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?", version):
        raise SystemExit("Cannot determine the release version from VERSION")
    prefix = f"Sakura-Bot-v{version}"
    archives = [args.directory / f"{prefix}{suffix}" for suffix in (".zip", ".tar.gz")]
    checksum_file = args.directory / "SHA256SUMS"
    paths, expected = source_paths(revision)
    if args.command == "build":
        build(revision, paths, archives, checksum_file, prefix)
    verify(archives, checksum_file, prefix, expected, revision, args.extract_to)


if __name__ == "__main__":
    main()
