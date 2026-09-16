"""Verify migration and boot against a private, network-isolated database copy."""
import argparse
import hashlib
import json
import os
import secrets
import subprocess
import time
from pathlib import Path
from urllib.request import urlopen

parser = argparse.ArgumentParser()
parser.add_argument('step', choices=['prepare', 'start', 'check'])
args = parser.parse_args()
os.umask(0o077)
root = Path('/opt/chengchuan-relay')
release = root / 'releases/optional-controls-20260915'
evidence = root / 'evidence/optional-controls-20260915'
db, cache, app, network = ['optional-release-' + name + '-20260915' for name in ['db','redis','app','network']]

def run(command, **kwargs):
    return subprocess.check_output(command, **kwargs)

def sql(statement):
    return run(['docker','exec',db,'psql','-U','relay','-d','relay','-At','-v','ON_ERROR_STOP=1','-c',statement]).decode().strip()

def fingerprint():
    return {table: sql("SELECT md5(COALESCE(string_agg(row_to_json(t)::text,E'\\n' ORDER BY id),'')) FROM " + table + ' t')
            for table in ['users','accounts','api_keys','usage_logs','account_tests']}

if args.step == 'prepare':
    settings = dict(line.split('=',1) for line in (release/'source/deploy/.env').read_text().splitlines() if line and not line.startswith('#') and '=' in line)
    run(['docker','network','create','--internal',network])
    postgres_env = release/'stage-postgres.env'
    password = secrets.token_urlsafe(30)
    postgres_env.write_text('POSTGRES_USER=relay\nPOSTGRES_DB=relay\nPOSTGRES_PASSWORD='+password+'\n')
    run(['docker','run','-d','--name',db,'--network',network,'--network-alias','postgres','--memory','768m','--env-file',str(postgres_env),'postgres:18-alpine'])
    run(['docker','run','-d','--name',cache,'--network',network,'--network-alias','redis','--memory','128m','redis:7-alpine'])
    ready = False
    for _ in range(40):
        if subprocess.run(['docker','exec',db,'pg_isready','-U','relay'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode == 0:
            ready=True; break
        time.sleep(1)
    assert ready, 'staging database did not start'
    with (root/'backups/before-optional-controls-20260915/database-before.dump').open('rb') as dump:
        subprocess.run(['docker','exec','-i',db,'pg_restore','-U','relay','-d','relay','--no-owner','--no-acl','--exit-on-error'],stdin=dump,check=True)
    before = fingerprint()
    original = sql("SELECT md5(COALESCE(string_agg(to_jsonb(t)::text,E'\\n' ORDER BY id),'')) FROM account_tests t")
    migration = (release/'source/backend/migrations/248_intelligent_assessment_v2.sql').read_text().strip()
    checksum = hashlib.sha256(migration.encode()).hexdigest()
    # Run the exact new migration transactionally on the copy, then confirm idempotency.
    statement = "BEGIN;"+migration+"\nINSERT INTO schema_migrations(filename,checksum) VALUES ('248_intelligent_assessment_v2.sql','"+checksum+"');COMMIT;"
    run(['docker','exec','-i',db,'psql','-U','relay','-d','relay','-v','ON_ERROR_STOP=1'],input=statement.encode())
    run(['docker','exec','-i',db,'psql','-U','relay','-d','relay','-v','ON_ERROR_STOP=1'],input=('BEGIN;'+migration+'\nCOMMIT;').encode())
    after = fingerprint()
    # account_tests gained scheduling columns, compare original fields separately below.
    unchanged = all(before[t] == after[t] for t in ['users','accounts','api_keys','usage_logs'])
    assert unchanged, 'migration altered unrelated business rows'
    account_tests_original = sql("SELECT md5(COALESCE(string_agg((to_jsonb(t)-'available_at'-'queue_reason')::text,E'\\n' ORDER BY id),'')) FROM account_tests t")
    assert original == account_tests_original, 'historical test fields changed'
    env = {'AUTO_SETUP':'true','SERVER_HOST':'0.0.0.0','SERVER_PORT':'8080','SERVER_MODE':'release','RUN_MODE':'standard',
           'DATABASE_HOST':'postgres','DATABASE_PORT':'5432','DATABASE_USER':'relay','DATABASE_PASSWORD':password,'DATABASE_DBNAME':'relay','DATABASE_SSLMODE':'disable',
           'REDIS_HOST':'redis','REDIS_PORT':'6379','REDIS_PASSWORD':'','JWT_SECRET':secrets.token_hex(32),'TOTP_ENCRYPTION_KEY':settings['TOTP_ENCRYPTION_KEY'],
           'ADMIN_EMAIL':settings['ADMIN_EMAIL'],'ADMIN_PASSWORD':settings['ADMIN_PASSWORD'],'TZ':'Asia/Hong_Kong'}
    (release/'stage-app.env').write_text('\n'.join(k+'='+v for k,v in env.items())+'\n')
    result={'isolated_network':network,'migration':'248_intelligent_assessment_v2.sql','checksum':checksum,'repeat_passed':True,'business_rows_preserved':unchanged,'historical_tests_preserved':original==account_tests_original}
    (evidence/'stage-migration.json').write_text(json.dumps(result,indent=2))
    print(json.dumps(result))
elif args.step == 'start':
    run(['docker','run','-d','--name',app,'--network',network,'--memory','1536m','--cpus','1.5','--security-opt','no-new-privileges:true','--env-file',str(release/'stage-app.env'),'-p','127.0.0.1:18085:8080','chengchuan-relay:optional-controls-20260915'])
    print('ISOLATED_STAGE_STARTED')
else:
    app_info = json.loads(run(['docker','inspect',app]))[0]
    internal_ip = app_info['NetworkSettings']['Networks'][network]['IPAddress']
    with urlopen('http://'+internal_ip+':8080/health',timeout=10) as response:
        assert response.status == 200 and json.load(response)['status'] == 'ok'
    result={'health':True,'internal_network':json.loads(run(['docker','network','inspect',network]))[0]['Internal'],
            'image':json.loads(run(['docker','inspect',app]))[0]['Image'],
            'migration_applied':sql("SELECT count(*) FROM schema_migrations WHERE filename='248_intelligent_assessment_v2.sql'") == '1'}
    assert result['internal_network'] and result['migration_applied']
    (evidence/'stage-boot.json').write_text(json.dumps(result,indent=2))
    print(json.dumps(result))
