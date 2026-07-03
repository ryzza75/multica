#!/usr/bin/env bash
#
# paperclip-launchd-wrapper.sh — foreground wrapper launchd supervises.
# Referenced by ing.paperclip.server.plist. Do not run directly for normal use;
# use paperclip-up.sh interactively instead.
#
# It reproduces the interactive shell environment (so brew python, node, and
# pnpm resolve exactly as they do in a terminal), clears stray watchers that
# would make the dev-runner defer to an orphan (runbook failure #9), then execs
# the server in the foreground so launchd's KeepAlive can supervise it.
#
set -uo pipefail

# Reproduce the login PATH (brew, ~/Library/pnpm, ~/.hermes/node, etc.).
[ -f "$HOME/.zprofile" ] && source "$HOME/.zprofile" 2>/dev/null || true
[ -f "$HOME/.zshrc" ]    && source "$HOME/.zshrc"    2>/dev/null || true
# Guarantee a 3.10+ python is first for the Hermes adapter env check.
export PATH="/opt/homebrew/bin:/usr/local/bin:${PATH}"

cd "$HOME/paperclip" || exit 1

pkill -f "tsx/dist/cli.mjs watch.*src/index.ts" 2>/dev/null || true
pkill -f "dev-runner.ts"                        2>/dev/null || true
pkill -f "dev-watch.ts"                         2>/dev/null || true
pkill -f "@paperclipai/server dev:watch"        2>/dev/null || true
for p in 3100 3101; do
  lsof -tnP -iTCP:$p -sTCP:LISTEN 2>/dev/null | xargs -r kill 2>/dev/null || true
done
sleep 2

export PAPERCLIP_HOME="$HOME/.paperclip"
export PAPERCLIP_INSTANCE_ID=default
exec pnpm dev --authenticated-private --bind loopback
