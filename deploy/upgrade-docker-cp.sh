#!/bin/sh
# Upgrade only the named, running application container. Keep a host-side backup.
set -eu

container=${1:?usage: upgrade-docker-cp.sh CONTAINER BINARY SHA256}
binary=${2:?binary path is required}
expected_sha=${3:?SHA256 is required}
case "$container" in ''|*[!a-zA-Z0-9_.-]*) echo 'Invalid container name' >&2; exit 1;; esac
case "$binary" in /*) ;; *) echo 'Use an absolute binary path' >&2; exit 1;; esac
[ -f "$binary" ] || { echo 'Binary not found' >&2; exit 1; }
actual_sha=$(sha256sum "$binary" | awk '{print $1}')
[ "$actual_sha" = "$expected_sha" ] || { echo 'SHA256 mismatch' >&2; exit 1; }
[ "$(docker inspect --format '{{.State.Running}}' "$container")" = true ] || {
    echo 'Restore the application to a running state before upgrading' >&2; exit 1;
}

backup_dir=$(mktemp -d /tmp/sub2api-upgrade.XXXXXXXX)
stage=/app/sub2api.upgrade-$(basename "$backup_dir")
installed=0
committed=0

finish() {
    result=$?
    trap - EXIT HUP INT TERM
    if [ "$installed" = 1 ] && [ "$committed" = 0 ]; then
        echo "Upgrade failed; restoring $backup_dir/sub2api" >&2
        docker stop --timeout 10 "$container" >/dev/null || true
        if docker cp "$backup_dir/sub2api" "$container:/app/sub2api" && docker start "$container" >/dev/null; then
            echo 'Previous binary restored; check application health.' >&2
        else
            echo "Automatic rollback failed. Backup: $backup_dir/sub2api" >&2
        fi
        result=1
    fi
    echo "Backup retained: $backup_dir/sub2api"
    exit "$result"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

docker cp "$container:/app/sub2api" "$backup_dir/sub2api"
chmod 0755 "$backup_dir/sub2api"
docker cp "$binary" "$container:$stage"
# Do not depend on Windows archive permissions or docker cp mode preservation.
docker exec -u 0 "$container" chmod 0755 "$stage"
docker exec -u 0 "$container" chown 0:0 "$stage"
staged_sha=$(docker exec "$container" sha256sum "$stage" | awk '{print $1}')
[ "$staged_sha" = "$expected_sha" ] || { echo 'Container SHA256 mismatch' >&2; exit 1; }
# Validate execution under the same unprivileged identity as the entrypoint.
docker exec -u sub2api "$container" "$stage" --version

installed=1
# Rename the staged binary; never truncate the running executable.
docker exec -u 0 "$container" mv -f "$stage" /app/sub2api
docker restart --timeout 20 "$container" >/dev/null

attempt=0
while [ "$attempt" -lt 30 ]; do
    status=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container")
    if [ "$status" = healthy ]; then
        committed=1
        echo "Upgrade healthy: $container SHA256=$expected_sha"
        exit 0
    fi
    attempt=$((attempt + 1))
    sleep 2
done
echo 'Container did not become healthy within 60 seconds' >&2
exit 1
