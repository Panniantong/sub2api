#!/bin/sh
set -eu
cd "$(dirname "$0")"
docker compose --env-file .env -f compose.relay.yml config --quiet
docker compose --env-file .env -f compose.relay.yml build app
docker compose --env-file .env -f compose.relay.yml up -d --wait --wait-timeout 180
python3 smoke-relay.py --env .env
