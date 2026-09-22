#!/usr/bin/env bash
set -Eeuo pipefail

MIHOMO_VERSION=${MIHOMO_VERSION:-v1.19.31}
MIHOMO_CODEX_SUBSCRIPTION_URL=${MIHOMO_CODEX_SUBSCRIPTION_URL:-}
MIHOMO_CODEX_USER_AGENT=${MIHOMO_CODEX_USER_AGENT:-clash.meta}
MIHOMO_CODEX_PORT=${MIHOMO_CODEX_PORT:-3101}
MIHOMO_CODEX_SECRET=${MIHOMO_CODEX_SECRET:-}

die() { echo "ERROR: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root"
[ -n "$MIHOMO_CODEX_SUBSCRIPTION_URL" ] || die "MIHOMO_CODEX_SUBSCRIPTION_URL is required"
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v gzip >/dev/null 2>&1 || die "gzip is required"

case "$(uname -m)" in
  x86_64|amd64) asset="mihomo-linux-amd64-compatible-${MIHOMO_VERSION}.gz" ;;
  aarch64|arm64) asset="mihomo-linux-arm64-${MIHOMO_VERSION}.gz" ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

id mihomo-codex >/dev/null 2>&1 || useradd --system --home-dir /var/lib/mihomo-codex --shell /usr/sbin/nologin mihomo-codex
install -d -o mihomo-codex -g mihomo-codex -m 0750 /var/lib/mihomo-codex /var/lib/mihomo-codex/providers
install -d -o root -g mihomo-codex -m 0750 /etc/mihomo-codex

binary_tmp=$(mktemp /tmp/mihomo.XXXXXX)
trap 'rm -f "$binary_tmp"' EXIT
curl -fsSL --max-time 120 \
  "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${asset}" \
  | gzip -d > "$binary_tmp"
test -s "$binary_tmp"
chmod 0755 "$binary_tmp"
install -o root -g root -m 0755 "$binary_tmp" /usr/local/bin/mihomo

if [ -n "$MIHOMO_CODEX_SECRET" ]; then
  local_secret=$MIHOMO_CODEX_SECRET
else
  # Read a fixed number of bytes so pipefail cannot turn a truncating pipeline
  # into a SIGPIPE failure while generating the local controller secret.
  local_secret=$(od -vAn -N24 -tx1 /dev/urandom | tr -d ' \n')
fi
cat > /etc/mihomo-codex/config.yaml <<EOF
mixed-port: ${MIHOMO_CODEX_PORT}
allow-lan: false
bind-address: 127.0.0.1
mode: rule
log-level: warning
ipv6: false
external-controller: 127.0.0.1:9098
secret: ${local_secret}
proxy-providers:
  airport:
    type: http
    url: "${MIHOMO_CODEX_SUBSCRIPTION_URL}"
    interval: 3600
    path: ./providers/airport.yaml
    header:
      User-Agent:
        - ${MIHOMO_CODEX_USER_AGENT}
    health-check:
      enable: true
      url: https://www.gstatic.com/generate_204
      interval: 300
      timeout: 5000
proxy-groups:
  - name: CODEX-ROTATE
    type: load-balance
    strategy: round-robin
    use: [airport]
    url: https://www.gstatic.com/generate_204
    interval: 180
    timeout: 5000
rules:
  - MATCH,CODEX-ROTATE
EOF
chown root:mihomo-codex /etc/mihomo-codex/config.yaml
chmod 0640 /etc/mihomo-codex/config.yaml
install -o root -g root -m 0644 "$(dirname "$0")/mihomo-codex.service" /etc/systemd/system/mihomo-codex.service
systemctl daemon-reload
/usr/local/bin/mihomo -d /var/lib/mihomo-codex -f /etc/mihomo-codex/config.yaml -t
systemctl enable --now mihomo-codex.service
echo "Mihomo is listening on http://127.0.0.1:${MIHOMO_CODEX_PORT}"
echo "Set the Sub2API 292 harvest proxy to that URL in the admin settings."
