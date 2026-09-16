"""Package only build inputs and source, with a SHA-256 inventory."""
import argparse
import hashlib
import io
import json
import os
import tarfile
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
output = args.output.resolve()
if root == output or root in output.parents:
    parser.error('output must be outside the source tree')
skip_dirs = {'node_modules', '.git', '__pycache__', 'dist', 'bin', 'data', 'postgres_data', 'redis_data', 'coverage', '.vite'}
files = []
for section in ['backend', 'frontend', 'docs/legal', 'deploy']:
    for directory, dirs, names in os.walk(root / section, followlinks=False):
        dirs[:] = [d for d in dirs if d not in skip_dirs and not Path(directory, d).is_symlink()]
        for name in names:
            path = Path(directory, name)
            if path.is_symlink() or name in {'config.yaml', 'config.local.yaml', 'PRIVATE_ACCESS.txt'}:
                continue
            if name.startswith('.env') and name != '.env.example':
                continue
            if path.suffix.lower() in {'.exe', '.zip', '.gz', '.dump', '.log', '.pyc', '.tsbuildinfo'} or name.endswith('.before-deploy'):
                continue
            files.append(path)
files.extend(root / name for name in ['Dockerfile', '.dockerignore', 'LICENSE'])
manifest = {p.relative_to(root).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(files)}
output.parent.mkdir(parents=True, exist_ok=True)
with tarfile.open(output, 'w:gz') as archive:
    for path in sorted(files):
        archive.add(path, arcname=path.relative_to(root).as_posix(), recursive=False)
    data = json.dumps({'algorithm': 'sha256', 'files': manifest}, indent=2).encode()
    entry = tarfile.TarInfo('RELEASE_SOURCE_MANIFEST.json')
    entry.size, entry.mode = len(data), 0o644
    archive.addfile(entry, io.BytesIO(data))
with tarfile.open(output) as archive:
    for name, expected in manifest.items():
        assert hashlib.sha256(archive.extractfile(name).read()).hexdigest() == expected, name
print(json.dumps({'archive': str(output), 'files': len(files), 'bytes': output.stat().st_size,
                  'sha256': hashlib.sha256(output.read_bytes()).hexdigest()}, ensure_ascii=False))
