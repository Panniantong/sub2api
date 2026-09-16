"""Prepare this release and verified backups without changing the running app."""
import hashlib
import json
import os
import shutil
import subprocess
import tarfile
from pathlib import Path

os.umask(0o077)
root = Path('/opt/chengchuan-relay')
release = root / 'releases/optional-controls-20260915'
backup = root / 'backups/before-optional-controls-20260915'
evidence = root / 'evidence/optional-controls-20260915'
current = root / 'feature-source-next'
archive = release / 'source.tar.gz'
assert hashlib.sha256(archive.read_bytes()).hexdigest() == 'c67137e3567f9b59d7069af74bd52d489edbf7e85625f5c4f1df1a2955ed444f'
source = release / 'source'
source.mkdir(exist_ok=False)
with tarfile.open(archive) as package:
    for entry in package.getmembers():
        assert entry.isfile() and not entry.name.startswith('/') and '..' not in Path(entry.name).parts
    package.extractall(source, filter='data')
manifest = json.loads((source / 'RELEASE_SOURCE_MANIFEST.json').read_text())['files']
assert all(hashlib.sha256((source / name).read_bytes()).hexdigest() == value for name, value in manifest.items())
new_migrations, modified_migrations = [], []
for path in sorted((source / 'backend/migrations').glob('*.sql')):
    old = current / 'backend/migrations' / path.name
    if not old.exists():
        new_migrations.append(path.name)
    elif path.read_bytes() != old.read_bytes():
        modified_migrations.append(path.name)
assert not modified_migrations, modified_migrations
assert new_migrations == ['248_intelligent_assessment_v2.sql'], new_migrations
shutil.copy2(current / 'deploy/.env', backup / 'app.env')
shutil.copy2(current / 'deploy/compose.relay.yml', backup / 'compose.relay.yml')
with (backup / 'source-before.tar.gz').open('xb') as output:
    subprocess.run(['tar', '-czf', '-', '-C', str(current), '.'], stdout=output, check=True)
with (backup / 'database-before.dump').open('xb') as output:
    subprocess.run(['docker', 'exec', 'chengchuan-relay-postgres-1', 'pg_dump', '-U', 'relay', '-d', 'relay', '-Fc'], stdout=output, check=True)
with (backup / 'database-before.dump').open('rb') as dump:
    listing = subprocess.check_output(['docker', 'exec', '-i', 'chengchuan-relay-postgres-1', 'pg_restore', '--list'], stdin=dump)
assert b'TABLE DATA' in listing
data = subprocess.check_output(['docker', 'inspect', 'chengchuan-relay-app-1'])
(backup / 'app-inspect.private.json').write_bytes(data)
app = json.loads(data)[0]
volume = next(m['Source'] for m in app['Mounts'] if m['Destination'] == '/app/data')
with (backup / 'app-data.tar.gz').open('xb') as output:
    subprocess.run(['tar', '-czf', '-', '-C', volume, '.'], stdout=output, check=True)
shutil.copy2(current / 'deploy/.env', source / 'deploy/.env')
settings = (source / 'deploy/.env').read_text().splitlines()
settings = [line for line in settings if not line.startswith('RELAY_VERSION=')]
settings.append('RELAY_VERSION=optional-controls-20260915')
(source / 'deploy/.env').write_text('\n'.join(settings) + '\n')
subprocess.run(['docker','compose','--env-file','.env','-f','compose.relay.yml','config','--quiet'],cwd=source/'deploy',check=True)
result = {'old_image': app['Image'], 'old_tag': app['Config']['Image'], 'release': str(source),
          'new_migrations': new_migrations, 'modified_migrations': modified_migrations,
          'source_files_verified': len(manifest), 'backups': {}}
for name in ['source-before.tar.gz', 'database-before.dump', 'app-data.tar.gz']:
    path = backup / name
    result['backups'][name] = {'bytes': path.stat().st_size, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
(evidence / 'preparation.json').write_text(json.dumps(result, indent=2))
print(json.dumps(result, indent=2))
