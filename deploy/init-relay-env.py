#!/usr/bin/env python3
"""Initialize private deployment secrets without printing them or overwriting."""
import argparse
import os
import secrets
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--output', type=Path, default=Path(__file__).with_name('.env'))
parser.add_argument('--bind', default='127.0.0.1')
parser.add_argument('--port', type=int, default=8080)
parser.add_argument('--admin-email', default='admin@relay.local')
args = parser.parse_args()
if not 1 <= args.port <= 65535:
    parser.error('port must be between 1 and 65535')
for value in (args.bind, args.admin_email):
    if any(c in value for c in '\r\n\x00'):
        parser.error('values must be single lines')
values = {
    'RELAY_VERSION': 'relay-v3-dev',
    'BIND_HOST': args.bind,
    'SERVER_PORT': str(args.port),
    'ADMIN_EMAIL': args.admin_email,
    'ADMIN_PASSWORD': secrets.token_urlsafe(24),
    'POSTGRES_PASSWORD': secrets.token_hex(32),
    'REDIS_PASSWORD': secrets.token_hex(32),
    'JWT_SECRET': secrets.token_hex(32),
    'TOTP_ENCRYPTION_KEY': secrets.token_hex(32),
}
fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as output:
    for key, value in values.items():
        output.write(f'{key}={value}\n')
print(f'Created private deployment config: {args.output}')
