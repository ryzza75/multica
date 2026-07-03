#!/usr/bin/env bash
#
# paperclip-up.sh — bring Paperclip up cleanly on the Mac mini (jarvis).
#
# Target end state:
#   - default instance (~/.paperclip/instances/default), NOT a stray test/worktree instance
#   - authenticated/private mode, bound to loopback 127.0.0.1:3100
#   - reachable from the phone via BOTH the device hostname
#     (https://ryans-mac-mini.tail195627.ts.net) and the svc:paperclip service
#     (https://paperclip.tail195627.ts.net)
#
# Paperclip took port 3100 over from Multica after Multica was decommissioned
# (see docs/paperclip-mac-mini-runbook.md). The port itself lives in
# ~/.paperclip/instances/default/config.json — this script does not set it; it
# assumes config.json already says 3100.
#
# Safe to re-run. Kills the orphaned dev watchers that caused the port roulette
# on 2026-07-03 (see docs/paperclip-mac-mini-runbook.md failure #9), then starts
# ONE clean server pinned to the real instance.
#
# It deliberately does NOT touch:
#   - the Hermes gateway (python hermes_cli gateway run)
#   - the Claude desktop app
#
set -uo pipefail

REPO="$HOME/paperclip"
LOG="$HOME/paperclip-dev.log"
PORT=3100
FALLBACK_PORT=3101
SERVICE_HOST="paperclip.tail195627.ts.net"
DEVICE_HOST="ryans-mac-mini.tail195627.ts.net"

# Ensure a modern python3 (adapter env check needs 3.10+) and brew tools resolve first.
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

cd "$REPO" || { echo "FATAL: $REPO not found"; exit 1; }

echo "==> 1. stopping existing paperclip dev processes + orphaned watchers"
pnpm dev:stop 2>/dev/null || true
pkill -f "tsx/dist/cli.mjs watch.*src/index.ts" 2>/dev/null || true
pkill -f "dev-runner.ts"                        2>/dev/null || true
pkill -f "dev-watch.ts"                         2>/dev/null || true
pkill -f "@paperclipai/server dev:watch"        2>/dev/null || true
for p in "$PORT" "$FALLBACK_PORT"; do
  lsof -tnP -iTCP:$p -sTCP:LISTEN 2>/dev/null | xargs -r kill 2>/dev/null || true
done
sleep 2

echo "==> 2. verifying ports are clear"
if lsof -nP -iTCP:$PORT -sTCP:LISTEN 2>/dev/null | grep -q LISTEN; then
  echo "    WARNING: something still holds $PORT — inspect with: lsof -nP -iTCP:$PORT -sTCP:LISTEN"
fi

echo "==> 3. python in use for the adapter env check:"
python3 --version || echo "    WARNING: python3 not found on PATH"

echo "==> 4. starting default instance (authenticated/private, loopback:$PORT)"
PAPERCLIP_HOME="$HOME/.paperclip" PAPERCLIP_INSTANCE_ID=default \
  nohup pnpm dev --authenticated-private --bind loopback < /dev/null > "$LOG" 2>&1 &

echo "==> 5. waiting for health on 127.0.0.1:$PORT ..."
up=""
for _ in $(seq 1 40); do
  if curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; then
    up="$(curl -s http://127.0.0.1:$PORT/api/health | head -c 160)"
    break
  fi
  sleep 2
done
if [ -n "$up" ]; then
  echo "    UP: $up"
else
  echo "    FAILED to come up on $PORT. Check: tail -30 $LOG"
  exit 1
fi

echo "==> 6. (re)asserting Tailscale serves -> $PORT (device hostname + svc:paperclip)"
tailscale serve --bg --https=443 "http://127.0.0.1:$PORT" \
  || echo "    NOTE: device serve failed — run it manually: tailscale serve --bg --https=443 http://127.0.0.1:$PORT"
tailscale serve --service=svc:paperclip --https=443 "http://127.0.0.1:$PORT" \
  || echo "    NOTE: service serve failed — run it manually and check the Services page."

echo
echo "==> DONE."
echo "    Phone test : https://$DEVICE_HOST/api/health"
echo "    Phone test : https://$SERVICE_HOST/api/health"
echo "    Board claim (if 'No company access'):"
echo "      open  https://$SERVICE_HOST/\$(grep -o 'board-claim/[^ ]*' $LOG | tail -1)"
