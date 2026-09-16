"""Package this local source and verify every archive member. No network access."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import tarfile

root = Path(__file__).resolve().parents[1]
out = Path(r"C:\Users\ROG\Desktop\sub2模式一本地成果") / datetime.datetime.now().strftime("mode1-%Y%m%d-%H%M%S")
out.mkdir(parents=True, exist_ok=False)
excluded_dirs = {".git", "node_modules", "dist", "build", "coverage", ".cache", "__pycache__", ".pnpm-store", "data"}
excluded_suffixes = {".exe", ".dll", ".db", ".sqlite", ".pem", ".key", ".p12", ".pfx", ".tsbuildinfo"}
entries = []
for current, dirs, files in os.walk(root, followlinks=False):
    dirs[:] = sorted(d for d in dirs if d not in excluded_dirs and not (Path(current) / d).is_symlink())
    for name in sorted(files):
        path = Path(current) / name
        if name.startswith(".env") or path.suffix.lower() in excluded_suffixes or path.is_symlink():
            continue
        if name.lower() in {"auth.json", "credentials.json", "cookies.txt"} or name.endswith((".tar.gz", ".zip")):
            continue
        entries.append((path.relative_to(root).as_posix(), path))
entries.sort()
manifest = []
archive = out / "source.tar.gz"
with tarfile.open(archive, "w:gz") as tar:
    for rel, path in entries:
        data = path.read_bytes()
        manifest.append({"path": rel, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
        tar.add(path, arcname="source/" + rel, recursive=False)
with tarfile.open(archive, "r:gz") as tar:
    members = tar.getmembers()
    assert len(members) == len(manifest)
    for expected, member in zip(manifest, members):
        assert member.isfile() and member.name == "source/" + expected["path"]
        assert hashlib.sha256(tar.extractfile(member).read()).hexdigest() == expected["sha256"]
digest = hashlib.sha256(archive.read_bytes()).hexdigest()
(out / "FILES.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
(out / "SHA256SUMS.txt").write_text("\n".join(f"{m['sha256']}  source/{m['path']}" for m in manifest) + "\n", encoding="utf-8")
(out / "ARCHIVE.sha256").write_text(digest + "  source.tar.gz\n", encoding="utf-8")
(out / "MODE1_LOCAL_HANDOFF.md").write_bytes((root / "MODE1_LOCAL_HANDOFF.md").read_bytes())
print(json.dumps({"directory": str(out), "files": len(entries), "sha256": digest, "archive_verified": True}, ensure_ascii=True))
