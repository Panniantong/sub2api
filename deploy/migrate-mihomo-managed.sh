#!/usr/bin/env bash
# Run automatically by the root binary installer. No subscription arguments.
set -Eeuo pipefail
[[ $(id -u) == 0 ]] || { echo 'Migration requires root' >&2; exit 1; }
migration_install_dir=${INSTALL_DIR:-/opt/sub2api}
migration_data_dir=${DATA_DIR:-$migration_install_dir}
[[ "$migration_data_dir" = /* && "$migration_data_dir" != / ]] || { echo 'Use an absolute Sub2API data directory' >&2; exit 1; }
[[ -f /etc/mihomo-codex/config.yaml ]] || exit 0
[[ ! -e "$migration_data_dir/mihomo-codex" ]] || exit 0
systemctl is-active --quiet mihomo-codex.service || exit 0
getent passwd sub2api >/dev/null
"$migration_install_dir/sub2api" --migrate-mihomo "$migration_data_dir"
chown -R sub2api:sub2api "$migration_data_dir/mihomo-codex"
# The old service remains available for explicit binary rollback; only its
# autostart is disabled after the replacement is healthy.
systemctl stop mihomo-codex.service
if ! systemctl restart sub2api.service; then
  mv -- "$migration_data_dir/mihomo-codex" "$migration_data_dir/mihomo-codex.failed-$(date +%s)"
  systemctl start mihomo-codex.service
  systemctl start sub2api.service || true
  echo 'Application restart failed; legacy proxy restarted' >&2
  exit 1
fi
for ((migration_attempt=0; migration_attempt<30; migration_attempt++)); do
  if systemctl is-active --quiet sub2api.service && "$migration_install_dir/sub2api" --check-managed-mihomo "$migration_data_dir" >/dev/null 2>&1; then
    systemctl disable mihomo-codex.service
    echo 'Mihomo ownership migrated to Sub2API; old files retained'
    exit 0
  fi
  sleep 1
done
systemctl stop sub2api.service
mv -- "$migration_data_dir/mihomo-codex" "$migration_data_dir/mihomo-codex.failed-$(date +%s)"
systemctl start mihomo-codex.service
systemctl start sub2api.service
echo 'Managed proxy did not become ready; legacy proxy restarted. Inspect the application before retrying.' >&2
exit 1
