"""Switch only the app after verified preparation; retain an immediate image rollback."""
import json
import os
import subprocess
import time
from pathlib import Path

os.umask(0o077)
root=Path('/opt/chengchuan-relay')
release=root/'releases/optional-controls-20260915'
deploy=release/'source/deploy'
evidence=root/'evidence/optional-controls-20260915'
backup=root/'backups/before-optional-controls-20260915'
assert (evidence/'build.exit').read_text().strip()=='0'
assert json.loads((evidence/'stage-http.json').read_text())['passed']
assert json.loads((evidence/'stage-migration.json').read_text())['historical_tests_preserved']

def sql(statement):
    return subprocess.check_output(['docker','exec','chengchuan-relay-postgres-1','psql','-U','relay','-d','relay','-At','-v','ON_ERROR_STOP=1','-c',statement]).decode().strip()

def configuration_hash():
    return sql("SELECT md5(COALESCE(string_agg(jsonb_build_array(id,concurrency,proxy_id,extra)::text,E'\\n' ORDER BY id),'')) FROM accounts")

def inspect(name):
    return json.loads(subprocess.check_output(['docker','inspect',name]))[0]

old=inspect('chengchuan-relay-app-1')
dependencies={name:inspect(name)['Id'] for name in ['chengchuan-relay-postgres-1','chengchuan-relay-redis-1']}
assert old['Image']=='sha256:3140765d60ec62efad35a922cbc5a1fdc4de85ea77e47b8b6ebd217dc07c50cd'
config_before=configuration_hash()
started=time.time()
subprocess.run(['docker','stop','--time','60','chengchuan-relay-app-1'],check=True,stdout=subprocess.DEVNULL)
try:
    with (backup/'database-at-cutover.dump').open('xb') as output:
        subprocess.run(['docker','exec','chengchuan-relay-postgres-1','pg_dump','-U','relay','-d','relay','-Fc'],stdout=output,check=True)
    command=['docker','compose','--env-file','.env','-f','compose.relay.yml','up','-d','--no-deps','--no-build','--wait','--wait-timeout','180','app']
    with (evidence/'cutover.log').open('w') as log:
        subprocess.run(command,cwd=deploy,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=220)
    with (evidence/'production-http.log').open('w') as log:
        subprocess.run(['python3',str(release/'check-release.py'),'--base-url','https://juhe.pub','--env',str(deploy/'.env'),'--output',str(evidence/'production-http.json'),'--db-container','chengchuan-relay-postgres-1'],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=180)
    current=inspect('chengchuan-relay-app-1')
    config_after=configuration_hash()
    assert config_before==config_after,'account configuration changed during cutover; investigate before accepting'
    assert all(inspect(name)['Id']==cid for name,cid in dependencies.items())
    result={'version':'optional-controls-20260915','image':current['Image'],'healthy':current['State']['Health']['Status']=='healthy',
            'account_configuration_preserved':config_before==config_after,'postgres_and_redis_not_recreated':True,
            'migration_applied':sql("SELECT count(*) FROM schema_migrations WHERE filename='248_intelligent_assessment_v2.sql'")=='1',
            'strict_rpm_enabled_accounts':int(sql("SELECT count(*) FROM accounts WHERE extra->'account_traffic_control'->>'strict_rpm_enabled'='true'")),
            'adaptive_enabled_accounts':int(sql("SELECT count(*) FROM accounts WHERE extra->'account_traffic_control'->>'adaptive_enabled'='true'")),
            'elapsed_seconds':round(time.time()-started,1),'url':'https://juhe.pub'}
    assert result['healthy'] and result['migration_applied']
    (evidence/'activation.json').write_text(json.dumps(result,indent=2))
    print(json.dumps(result,indent=2))
except Exception as error:
    rollback_env=backup/'rollback.env'
    lines=(backup/'app.env').read_text().splitlines()
    lines=[line for line in lines if not line.startswith('RELAY_VERSION=')]
    lines.append('RELAY_VERSION=rollback-optional-controls-20260915')
    rollback_env.write_text('\n'.join(lines)+'\n')
    with (evidence/'rollback.log').open('w') as log:
        subprocess.run(['docker','compose','--env-file',str(rollback_env),'-f',str(backup/'compose.relay.yml'),'up','-d','--no-deps','--no-build','--wait','--wait-timeout','180','app'],cwd=backup,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=220)
    print('APPLICATION_ROLLED_BACK: '+type(error).__name__)
    raise
