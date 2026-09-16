"""Pinned-host SSH deployment session. Credentials are read privately, never saved."""
import argparse
import getpass
import json
import sys
import time
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--host', required=True)
parser.add_argument('--known-hosts', required=True)
parser.add_argument('--modules', required=True)
args = parser.parse_args()
sys.path.insert(0, args.modules)
import paramiko

client = paramiko.SSHClient()
client.load_host_keys(args.known_hosts)
client.set_missing_host_key_policy(paramiko.RejectPolicy())
password = getpass.getpass('SSH password: ')
client.connect(args.host, username='root', password=password, look_for_keys=False,
               allow_agent=False, timeout=20, auth_timeout=20, banner_timeout=20)
password = None
print('SSH_CONNECTED_PINNED_HOST', flush=True)
try:
    while True:
        line = sys.stdin.readline()
        if not line:
            break
        try:
            request = json.loads(line)
            action = request['action']
            if action == 'close':
                break
            if action == 'exec':
                channel = client.get_transport().open_session()
                channel.exec_command(request['command'])
                while True:
                    if channel.recv_ready():
                        print(channel.recv(32768).decode('utf-8', errors='replace'), end='', flush=True)
                    if channel.recv_stderr_ready():
                        print(channel.recv_stderr(32768).decode('utf-8', errors='replace'), end='', flush=True)
                    if channel.exit_status_ready() and not channel.recv_ready() and not channel.recv_stderr_ready():
                        break
                    time.sleep(0.1)
                print('\nREMOTE_EXIT=' + str(channel.recv_exit_status()), flush=True)
                channel.close()
            elif action in ('upload', 'download'):
                with client.open_sftp() as sftp:
                    if action == 'upload':
                        sftp.put(request['local'], request['remote'])
                    else:
                        Path(request['local']).parent.mkdir(parents=True, exist_ok=True)
                        sftp.get(request['remote'], request['local'])
                print('TRANSFER_COMPLETE', flush=True)
            else:
                raise ValueError('Unknown action')
        except Exception as error:
            print('SESSION_ACTION_ERROR=' + type(error).__name__ + ': ' + str(error), flush=True)
finally:
    client.close()
