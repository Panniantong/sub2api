#!/usr/bin/env python3
"""Run server-local smoke checks; never emit passwords, tokens or account data."""
import argparse
import json
import sys
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

parser = argparse.ArgumentParser()
parser.add_argument('--base-url', default='http://127.0.0.1:8080')
parser.add_argument('--env', type=Path, default=Path(__file__).with_name('.env'))
args = parser.parse_args()
settings = dict(line.split('=', 1) for line in args.env.read_text().splitlines()
                if line and not line.startswith('#') and '=' in line)
results = []

def request(path, *, method='GET', body=None, token=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = Request(args.base_url.rstrip('/') + path,
                  data=json.dumps(body).encode() if body is not None else None,
                  headers=headers, method=method)
    try:
        response = urlopen(req, timeout=30)
    except HTTPError as error:
        response = error
    with response:
        raw = response.read()
        try:
            data = json.loads(raw)
        except (ValueError, UnicodeDecodeError):
            data = {}
        return response.status, data, raw

def check(name, okay, **details):
    results.append({'name': name, 'passed': bool(okay), **details})

status, data, _ = request('/health')
check('health', status == 200 and data.get('status') == 'ok', status=status)
status, data, _ = request('/api/v1/settings/public')
check('public_settings', status == 200 and data.get('code') == 0, status=status)
status, _, page = request('/home')
check('frontend_shell', status == 200 and b'type="module"' in page, status=status)
status, _, _ = request('/api/v1/keys')
check('keys_require_auth', status == 401, status=status)
status, _, _ = request('/v1/responses', method='POST', body={'model': 'test', 'input': 'hello'})
check('gateway_requires_key', status == 401, status=status)
status, data, _ = request('/api/v1/auth/login', method='POST', body={
    'email': settings['ADMIN_EMAIL'], 'password': settings['ADMIN_PASSWORD']})
token = (data.get('data') or {}).get('access_token')
check('admin_login', status == 200 and bool(token), status=status)
if token:
    for path in ['/api/v1/user/profile', '/api/v1/keys', '/api/v1/groups/available',
                 '/api/v1/usage/dashboard/stats', '/api/v1/usage/dashboard/trend',
                 '/api/v1/usage/dashboard/models', '/api/v1/usage?page=1&page_size=5',
                 '/api/v1/user/platform-quotas']:
        status, data, _ = request(path, token=token)
        check(path, status == 200 and data.get('code') == 0, status=status)
    status, data, _ = request('/api/v1/admin/compliance', token=token)
    check('admin_confirmation_state_readable', status == 200, status=status)
print(json.dumps({'scope': 'server HTTP/auth/database-backed user APIs; no upstream account call',
                  'checks': results, 'passed': all(item['passed'] for item in results)},
                 ensure_ascii=False, indent=2))
sys.exit(0 if all(item['passed'] for item in results) else 1)
