#!/usr/bin/env bash
#
# paperclip-cutover.sh — one-shot "remove Multica, make Paperclip the sole app"
# for the Mac mini (jarvis). Run this ON THE MINI; it cannot be run remotely
# (the tailnet hostnames only resolve from a device on the tailnet).
#
# It does, in order:
#   1. Decommission Multica   — stop+remove the `multica` Docker stack
#                               (containers, pgdata + backend_uploads volumes,
#                               images) and clear its device-level tailscale
#                               serve config. DESTRUCTIVE: drops Multica's DB.
#   2. Move Paperclip → 3100  — set the port in config.json and allowlist both
#                               hostnames Paperclip is now served under.
#   3. Bring Paperclip up     — via paperclip-up.sh in this same directory
#                               (starts on 3100, asserts both tailscale serves).
#   4. Verify                 — Multica gone, ports right, local health is
#                               authenticated, and BOTH https URLs answer.
#
# Usage:
#   bash paperclip-cutover.sh            # full cutover (asks before the teardown)
#   bash paperclip-cutover.sh --yes      # full cutover, no prompt
#   bash paperclip-cutover.sh --verify   # run ONLY the verification checks
#
# See docs/paperclip-mac-mini-runbook.md for the manual equivalents and the
# failure modes each step guards against.
#
set -uo pipefail

PORT=3100
FALLBACK_PORT=3101
SERVICE_HOST="paperclip.tail195627.ts.net"
DEVICE_HOST="ryans-mac-mini.tail195627.ts.net"
MULTICA_REPO="$HOME/multica"
PAPERCLIP_HOME="${PAPERCLIP_HOME:-$HOME/.paperclip}"
CONFIG="$PAPERCLIP_HOME/instances/default/config.json"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

pass=0; fail=0
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
note() { printf '  ---- %s\n' "$1"; }

# ---------------------------------------------------------------------------
# Step 1 — decommission Multica
# ---------------------------------------------------------------------------
decommission_multica() {
  echo "==> 1. decommissioning Multica (Docker stack + device serve)"

  if ! command -v docker >/dev/null 2>&1; then
    note "docker not found — assuming Multica was never containerised here."
  else
    if [ -f "$MULTICA_REPO/docker-compose.selfhost.yml" ]; then
      ( cd "$MULTICA_REPO" && \
        docker compose -f docker-compose.selfhost.yml down -v --rmi all --remove-orphans ) \
        2>/dev/null || true
    fi
    # Belt-and-suspenders by project name, in case the checkout is already gone.
    docker compose -p multica down -v --rmi all --remove-orphans 2>/dev/null || true
  fi

  # Clear device-level tailscale serve (was machine-host -> 3100, /ws -> 8080).
  # paperclip-up.sh re-asserts the Paperclip device + service serves afterwards.
  if command -v tailscale >/dev/null 2>&1; then
    tailscale serve reset 2>/dev/null || true
  else
    note "tailscale CLI not found — clear its device serve config manually."
  fi
}

# ---------------------------------------------------------------------------
# Step 2 — move Paperclip's port to 3100 + allowlist both hostnames
# ---------------------------------------------------------------------------
configure_port_and_allowlist() {
  echo "==> 2. setting Paperclip port to $PORT and allowlisting both hostnames"

  if [ ! -f "$CONFIG" ]; then
    note "config.json not found at $CONFIG — has the default instance booted at least once?"
  else
    cp "$CONFIG" "$CONFIG.bak.cutover"
    python3 - "$CONFIG" "$PORT" <<'PY'
import json, sys
path, target = sys.argv[1], int(sys.argv[2])
with open(path) as f:
    cfg = json.load(f)
changed = []
def walk(node, trail=""):
    if isinstance(node, dict):
        for k, v in node.items():
            p = f"{trail}.{k}" if trail else k
            # Only touch a server-ish port: an int already in the 3xxx dev range.
            # This deliberately skips the embedded Postgres port (54329) etc.
            if k.lower() == "port" and isinstance(v, int) and 3000 <= v <= 3299 and v != target:
                node[k] = target
                changed.append((p, v, target))
            else:
                walk(v, p)
    elif isinstance(node, list):
        for i, v in enumerate(node):
            walk(v, f"{trail}[{i}]")
walk(cfg)
if changed:
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2)
        f.write("\n")
    for p, old, new in changed:
        print(f"    port change: {p}: {old} -> {new}")
else:
    print(f"    no 3xxx port key needed changing (already {target}?) — verify below.")
PY
  fi

  # Allowlist via the CLI (safe, non-interactive). Read at boot, so this must
  # precede the restart in step 3. A configure run can wipe the list, so set both.
  if [ -d "$HOME/paperclip" ]; then
    ( cd "$HOME/paperclip" && \
      pnpm paperclipai allowed-hostname "$SERVICE_HOST" && \
      pnpm paperclipai allowed-hostname "$DEVICE_HOST" ) 2>/dev/null \
      || note "allowed-hostname CLI failed — add $SERVICE_HOST and $DEVICE_HOST manually."
  fi
}

# ---------------------------------------------------------------------------
# Step 4 — verification (also runnable standalone via --verify)
# ---------------------------------------------------------------------------
verify() {
  echo "==> verifying end state"

  # Multica containers gone.
  if command -v docker >/dev/null 2>&1; then
    if [ -z "$(docker ps -aq --filter name=multica 2>/dev/null)" ]; then
      ok "no Multica containers remain"
    else
      bad "Multica containers still present: $(docker ps -a --filter name=multica --format '{{.Names}}' | tr '\n' ' ')"
    fi
  fi

  # Backend port 8080 (Multica's) is free.
  if lsof -nP -iTCP:8080 -sTCP:LISTEN >/dev/null 2>&1; then
    bad "port 8080 still has a listener (was Multica's backend)"
  else
    ok "port 8080 is free"
  fi

  # Paperclip is the listener on 3100.
  if lsof -nP -iTCP:$PORT -sTCP:LISTEN >/dev/null 2>&1; then
    ok "something is listening on $PORT"
  else
    bad "nothing is listening on $PORT — Paperclip is not up"
  fi

  # Local health, authenticated mode.
  h="$(curl -sf "http://127.0.0.1:$PORT/api/health" 2>/dev/null || true)"
  if [ -z "$h" ]; then
    bad "no local health response on 127.0.0.1:$PORT/api/health"
  elif printf '%s' "$h" | grep -q '"deploymentMode":"authenticated"'; then
    ok "local health is authenticated: $(printf '%s' "$h" | head -c 120)"
  else
    bad "local health not in authenticated mode: $(printf '%s' "$h" | head -c 120)"
  fi

  # Both public URLs answer (resolves only from the tailnet — i.e. on the mini).
  for host in "$DEVICE_HOST" "$SERVICE_HOST"; do
    r="$(curl -sf -m 15 "https://$host/api/health" 2>/dev/null || true)"
    if printf '%s' "$r" | grep -q '"status":"ok"'; then
      ok "https://$host/api/health answers ok"
    else
      bad "https://$host/api/health did NOT answer (serve down, hostname not allowlisted, or DNS not on tailnet?)"
    fi
  done

  echo
  echo "==> RESULT: $pass passed, $fail failed"
  [ "$fail" -eq 0 ] && echo "    All green — Multica removed, Paperclip running on $PORT and accessible on both URLs."
  return "$fail"
}

# ---------------------------------------------------------------------------
main() {
  case "${1:-}" in
    --verify) verify; exit $? ;;
  esac

  if [ "${1:-}" != "--yes" ]; then
    echo "This REMOVES Multica from this machine, including its Postgres data"
    echo "volume (docker compose down -v). This cannot be undone."
    printf "Type 'remove multica' to continue: "
    read -r reply
    [ "$reply" = "remove multica" ] || { echo "Aborted."; exit 1; }
  fi

  decommission_multica
  configure_port_and_allowlist

  echo "==> 3. bringing Paperclip up on $PORT (paperclip-up.sh)"
  if [ -f "$SCRIPT_DIR/paperclip-up.sh" ]; then
    bash "$SCRIPT_DIR/paperclip-up.sh" || note "paperclip-up.sh reported a problem — see its output above."
  else
    bad "paperclip-up.sh not found next to this script ($SCRIPT_DIR) — copy the whole paperclip-mini/ dir over."
  fi

  echo
  verify
  exit $?
}

main "$@"
