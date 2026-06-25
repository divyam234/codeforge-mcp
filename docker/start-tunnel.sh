#!/bin/sh
set -eu

: "${CONTROL_PLANE_TUNNEL_ID:?Set CONTROL_PLANE_TUNNEL_ID in .env}"
: "${CONTROL_PLANE_API_KEY:?Set CONTROL_PLANE_API_KEY in .env}"

health_port="${TUNNEL_HEALTH_PORT:-8080}"
log_level="${TUNNEL_LOG_LEVEL:-info}"
log_format="${TUNNEL_LOG_FORMAT:-struct-text}"

mkdir -p /run/tunnel-client /var/lib/tunnel-client

exec /usr/local/bin/tunnel-client run \
  --control-plane.tunnel-id "${CONTROL_PLANE_TUNNEL_ID}" \
  --control-plane.api-key env:CONTROL_PLANE_API_KEY \
  --mcp.command "/usr/local/bin/codeforge-stdio" \
  --health.listen-addr "0.0.0.0:${health_port}" \
  --health.url-file /run/tunnel-client/health-url \
  --allow-remote-ui \
  --log.level "${log_level}" \
  --log.format "${log_format}"
