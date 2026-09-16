#!/usr/bin/env python3
"""Create a reviewable source inventory and archive, excluding local secrets."""
import argparse
import hashlib
import json
import zipfile
from pathlib import Path

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
output = args.output.resolve()
if output == root or root in output.parents:
    parser.error('output must be outside the source directory')

def included(path):
    rel = path.relative_to(root)
    if any(part in {'.git', 'node_modules', '__pycache__', '.tools'} for part in rel.parts):
        return False
    if str(rel).replace('\\', '/').startswith('backend/internal/web/dist/'):
        return False
    if path.name in {'PRIVATE_ACCESS.txt', 'config.yaml', 'relay.dump', 'DIRECTORY_TREE.txt', 'SOURCE_MANIFEST.json'}:
        return False
    if path.name.startswith('.env') and path.name != '.env.example':
        return False
    return path.is_file()

paths = sorted((p for p in root.rglob('*') if included(p)), key=lambda p: p.relative_to(root).as_posix())
manifest = {p.relative_to(root).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
inventory = root / 'DIRECTORY_TREE.txt'
inventory.write_text('Source root: ' + str(root) + '\nExcluded: .git, node_modules, generated web/dist, private env/config/access files\n\n' +
                     '\n'.join(manifest) + '\nDIRECTORY_TREE.txt\nSOURCE_MANIFEST.json\n', encoding='utf-8')
manifest_path = root / 'SOURCE_MANIFEST.json'
manifest_path.write_text(json.dumps({'algorithm': 'sha256', 'files': manifest}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
    for path in paths + [inventory, manifest_path]:
        archive.write(path, 'chengchuan-relay/' + path.relative_to(root).as_posix())
with zipfile.ZipFile(output, 'r') as archive:
    invalid = archive.testzip()
    if invalid is not None:
        raise RuntimeError(f'archive CRC verification failed: {invalid}')
digest = hashlib.sha256(output.read_bytes()).hexdigest()
output.with_suffix(output.suffix + '.sha256').write_text(digest + '  ' + output.name + '\n', encoding='ascii')
print(json.dumps({'archive': str(output), 'files': len(paths) + 2, 'sha256': digest}, ensure_ascii=False))
