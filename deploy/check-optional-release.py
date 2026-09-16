"""Read-only HTTP validation for the release; secrets stay in server memory."""
import argparse
import json
import re
import subprocess
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

parser = argparse.ArgumentParser()
parser.add_argument('--base-url', required=True)
parser.add_argument('--env', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--db-container', required=True)
args = parser.parse_args()
settings = dict(line.split('=',1) for line in args.env.read_text().splitlines() if line and not line.startswith('#') and '=' in line)
checks = []

def request(path, body=None, token=None):
    headers={'Content-Type':'application/json'}
    if token: headers['Authorization']='Bearer '+token
    req=Request(args.base_url.rstrip('/')+path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
    try: response=urlopen(req,timeout=25)
    except HTTPError as error: response=error
    with response:
        raw=response.read()
        try: data=json.loads(raw)
        except (ValueError,UnicodeDecodeError): data={}
        return response.status,data,raw,response.headers

def check(name, okay, **details):
    checks.append({'name':name,'passed':bool(okay),**details})

def sql(statement):
    return subprocess.check_output(['docker','exec',args.db_container,'psql','-U','relay','-d','relay','-At','-c',statement]).decode().strip()

status,data,_,_=request('/health')
check('health',status==200 and data.get('status')=='ok')
status,_,page,_=request('/home')
check('frontend_shell',status==200 and b'type="module"' in page)
assets=re.findall(rb'(?:src|href)="(/assets/[^"\s]+)"',page)
check('frontend_assets_listed',bool(assets))
for asset in assets[:8]:
    status,_,_,_=request(asset.decode())
    check('asset:'+asset.decode(),status==200)
for path in ['/api/v1/keys','/api/v1/admin/accounts/1/traffic-control','/api/v1/admin/groups/1/stats']:
    status,_,_,_=request(path)
    check('unauthenticated:'+path,status==401,status=status)
status,_,_,_=request('/v1/responses',{'model':'deployment-auth-check','input':'hello'})
check('gateway_requires_api_key',status==401,status=status)
status,data,_,_=request('/api/v1/auth/login',{'email':settings['ADMIN_EMAIL'],'password':settings['ADMIN_PASSWORD']})
token=(data.get('data') or {}).get('access_token')
check('existing_admin_login',status==200 and bool(token),status=status)
if token:
    status,data,_,_=request('/api/v1/user/profile',token=token)
    check('authenticated_profile',status==200 and data.get('code')==0,status=status)
    aid=sql("SELECT id FROM accounts WHERE deleted_at IS NULL ORDER BY id LIMIT 1")
    if aid:
        status,data,_,headers=request('/api/v1/admin/accounts/'+aid+'/traffic-control',token=token)
        value=data.get('data') or {}; policy=value.get('policy') or {}
        check('traffic_control_read',status==200 and isinstance(policy.get('strict_rpm_enabled'),bool) and isinstance(policy.get('adaptive_enabled'),bool) and value.get('state_available') is True and headers.get('Cache-Control')=='no-store',status=status)
    gid=sql("SELECT id FROM groups WHERE deleted_at IS NULL ORDER BY id LIMIT 1")
    if gid:
        status,data,_,headers=request('/api/v1/admin/groups/'+gid+'/stats',token=token)
        value=data.get('data') or {}
        required=['total_actual_cost','total_cost','total_account_cost','total_requests']
        check('real_group_stats',status==200 and all(isinstance(value.get(k),(int,float)) for k in required) and headers.get('Cache-Control')=='no-store',status=status)
        status,_,_,_=request('/api/v1/admin/groups/'+gid+'/stats?from=bad-date',token=token)
        check('group_stats_invalid_date',status==400,status=status)
    for path in ['/api/v1/admin/intelligent-tests/accounts','/api/v1/admin/intelligent-tests/settings']:
        status,data,_,_=request(path,token=token)
        check('test_center:'+path,status==200 and data.get('code')==0,status=status)
result={'base_url':args.base_url,'checks':checks,'passed':all(c['passed'] for c in checks),'scope':'health, assets, auth, read-only admin APIs; no model request with real credentials'}
args.output.write_text(json.dumps(result,ensure_ascii=False,indent=2))
print(json.dumps(result,ensure_ascii=False,indent=2))
raise SystemExit(0 if result['passed'] else 1)
