# Paperclip on the Mac mini — Setup & Recovery Runbook

Operational notes for the Paperclip instance (https://github.com/paperclipai/paperclip)
running on the Mac mini (`jarvis`, Tailscale machine name `ryans-mac-mini`).
Originally set up 2026-07-03 alongside Multica; on 2026-07-03 Multica was
**decommissioned** and Paperclip took its place as the sole app on the mini
(see "Decommissioning Multica" below). This file is unrelated to the Multica
codebase; it lives here so the working configuration and every failure mode we
hit (and fixed) is on record.

> **Note on the repo checkout.** These ops files live in the Multica repo under
> `docs/paperclip-mini/`. The mini no longer keeps a `~/multica` checkout after
> decommission, so copy the scripts you run there out to `~/paperclip` (or fetch
> them from GitHub). The launchd wrapper is already expected at
> `~/paperclip/paperclip-launchd-wrapper.sh`.

## Topology

```
Phone / laptop (Tailscale)
  ├─ https://ryans-mac-mini.tail195627.ts.net     (device-level tailscale serve, was Multica → now Paperclip)
  └─ https://paperclip.tail195627.ts.net           (Tailscale Service svc:paperclip, TLS by Tailscale)
       └─ tailscale serve proxy → 127.0.0.1:3100  (on the mini)
            └─ Paperclip dev server (authenticated/private, bind loopback)
                 └─ embedded PostgreSQL on 127.0.0.1:54329
                      data: ~/.paperclip/instances/default/
```

- Paperclip now owns port **3100** (reclaimed from Multica) and is served under
  **both** the device hostname `ryans-mac-mini.tail195627.ts.net` and the
  `svc:paperclip` service hostname `paperclip.tail195627.ts.net`. Both proxy to
  `127.0.0.1:3100`.
- Both of those hostnames must be in Paperclip's allowlist, or it returns 403
  (see failure #3). Ports **3100** (primary) and **3101** (the "next free port"
  Paperclip drifts to when 3100 is taken) are the pair to watch.
- Hermes Agent (`pip install hermes-agent aiohttp`) runs on the same mini.
  Local models via `~/.hermes/config.yaml`:

  ```yaml
  model:
    default: "<model-tag>"            # e.g. an ollama tag
    provider: "custom"                # aliases: ollama / vllm / llamacpp; LM Studio uses "lmstudio"
    base_url: "http://127.0.0.1:11434/v1"
  ```

  In Paperclip, Hermes agents use adapter `hermes_local` with model `auto`,
  which defers model/provider resolution to that file.

## Decommissioning Multica (done 2026-07-03)

Multica ran as the `multica` Docker Compose project (services `postgres`,
`backend`, `frontend`; named volumes `pgdata` + `backend_uploads`), with the
frontend on **3100** and the backend/websocket on **8080**, fronted by a
device-level `tailscale serve` config (`ryans-mac-mini.tail195627.ts.net` → 3100,
`/ws` → 8080). Removing it completely and freeing those ports for Paperclip:

```sh
# 1. Stop and remove the Multica stack completely — containers, network, the
#    named volumes (Postgres data + uploads) and the images. This is
#    destructive: -v drops the database volume. That is the intent here.
cd ~/multica
docker compose -f docker-compose.selfhost.yml down -v --rmi all --remove-orphans
#    If it had been started with the build override, drop those images too:
# docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml \
#     down -v --rmi all --remove-orphans
#    Belt-and-suspenders by project name, in case the compose files moved:
docker compose -p multica down -v --rmi all --remove-orphans 2>/dev/null || true

# 2. Confirm nothing multica-shaped is left and 3100 / 8080 are free.
docker ps -a --filter name=multica
lsof -nP -iTCP:3100 -sTCP:LISTEN    # expect empty
lsof -nP -iTCP:8080 -sTCP:LISTEN    # expect empty

# 3. Clear Multica's device-level tailscale serve config (machine hostname
#    → 3100 and /ws → 8080). Simplest and version-proof: reset the device-level
#    serve, then re-add only Paperclip (next section). `reset` clears device
#    web-serve mappings; the svc:paperclip *service* serve is re-asserted by
#    paperclip-up.sh regardless.
tailscale serve reset

# 4. (optional) drop the now-unused Multica checkout AFTER copying these ops
#    scripts somewhere that survives — e.g. `cp -r ~/multica/docs/paperclip-mini ~/paperclip/ops`.
# rm -rf ~/multica
```

If any Multica launchd/login-item auto-start exists, unload it too
(`launchctl list | grep -i multica` → `launchctl unload <plist>`), so it does
not resurrect the containers or re-grab port 3100 on reboot.

## Move Paperclip onto port 3100

Paperclip's port lives in `~/.paperclip/instances/default/config.json`, not in a
CLI flag (`pnpm dev` only takes `--authenticated-private --bind loopback`). Set
it to 3100 and add both hostnames to the allowlist. Do this from `~/paperclip`:

```sh
cd ~/paperclip

# Set the server port to 3100 (interactive; or edit config.json "port": 3100).
pnpm paperclipai configure --section server

# A `configure --section server` run can rewrite the allowlist, so (re)add BOTH
# hostnames Paperclip is now served under, AFTER configuring:
pnpm paperclipai allowed-hostname paperclip.tail195627.ts.net
pnpm paperclipai allowed-hostname ryans-mac-mini.tail195627.ts.net

# The allowlist and port are read at boot only — restart the server after.
```

Then bring it up with `paperclip-up.sh` (below), which starts the server on 3100
and re-asserts both the device serve and the service serve.

## Quick recovery: `paperclip-up.sh`

`docs/paperclip-mini/paperclip-up.sh` does the full clean bring-up in one shot:
kills orphaned watchers, frees the ports, starts the default instance
(authenticated/private, loopback:3100), and re-asserts both Tailscale serves
(device hostname + `svc:paperclip` service). Copy it to the mini and run it
whenever Paperclip is misbehaving or after a reboot:

```sh
bash ~/paperclip/ops/paperclip-up.sh   # or wherever you copied it
```

The manual equivalents are below.

## Start / stop (until launchd service exists)

All `pnpm` commands must run from the paperclip checkout: `cd ~/paperclip`.

```sh
# start (survives terminal close; "< /dev/null" prevents tty suspension — see failure #5)
nohup pnpm dev --authenticated-private --bind loopback < /dev/null > ~/paperclip-dev.log 2>&1 &

# stop
pnpm dev:stop

# health
curl -s http://127.0.0.1:3100/api/health                            # local
curl -s https://paperclip.tail195627.ts.net/api/health              # via service hostname
curl -s https://ryans-mac-mini.tail195627.ts.net/api/health         # via device hostname
# expect: {"status":"ok","deploymentMode":"authenticated",...}
```

The exact flag pair matters: the dev runner **ignores**
`PAPERCLIP_DEPLOYMENT_MODE`/`PAPERCLIP_DEPLOYMENT_EXPOSURE` env vars (it
deletes them) — mode is set only via its flags. `--authenticated-private`
turns auth on, `--bind loopback` keeps it on 127.0.0.1 behind the proxy.

Port 3100 and the hostname allowlist live in
`~/.paperclip/instances/default/config.json` (managed via
`pnpm paperclipai configure --section server` and
`pnpm paperclipai allowed-hostname <host>`).

## Tailscale serves (both hostnames → 3100)

Two serves now point at Paperclip. Re-run either any time it goes missing —
neither is guaranteed to survive Tailscale restarts (see failure #7):

```sh
# Device hostname (the URL Multica used to answer on):
tailscale serve --bg --https=443 http://127.0.0.1:3100

# Service hostname (svc:paperclip):
tailscale serve --service=svc:paperclip --https=443 http://127.0.0.1:3100
```

One-time `svc:paperclip` state, already done — recorded here for rebuilds:

1. Admin console → Services → service `paperclip` (port tcp:443)
   → `paperclip.tail195627.ts.net`, VIP 100.85.250.255.
2. ACL policy additions (Access Controls):

   ```json
   "tagOwners":     { "tag:server": ["autogroup:admin"] },
   "autoApprovers": { "services": { "svc:paperclip": ["tag:server"] } },
   "grants":        [ {"src": ["*"], "dst": ["svc:paperclip"], "ip": ["*"]} ]
   ```

3. The mini is tagged `tag:server` (Machines → Edit ACL tags; service hosts
   must be tagged nodes).

## Failure modes we hit, and their fixes

1. **Wrong app answers on the URL / a stale page appears** — the proxy target
   port is occupied by something else. `lsof -nP -iTCP:3100 -sTCP:LISTEN` and
   check which process owns it; Paperclip auto-shifts to the next free port
   when its port is taken (watch startup log for
   `Requested port is busy; using next free port`), which silently breaks the
   proxy. Kill strays, keep Paperclip on 3100. (Post-decommission the old
   Multica containers should be gone; if a Multica page reappears, a container
   restarted — re-run the decommission `down` and check for a Multica login
   item.)

2. **Health shows `"deploymentMode":"local_trusted"`** — server started
   without the mode flags (env vars don't work; see above), or an old
   instance was still running so `pnpm dev` no-op'd
   (`paperclip-dev-watch already running`). `pnpm dev:stop`, kill leftovers,
   restart with the exact nohup line.

3. **403 "Hostname ... is not allowed"** — allowlist entry missing (a
   `configure --section server` run can rewrite the server config section).
   Both hostnames must be present. Fix:
   `pnpm paperclipai allowed-hostname paperclip.tail195627.ts.net` and
   `pnpm paperclipai allowed-hostname ryans-mac-mini.tail195627.ts.net`, then
   restart the server — the allowlist is read at boot only.

4. **Embedded Postgres won't start: `postmaster.pid is empty` / port 54329
   busy** — crash remnant or orphaned postgres. Check
   `lsof -nP -iTCP:54329 -sTCP:LISTEN`; stop a live one with `kill -INT <pid>`
   (fast shutdown, safe; never `kill -9` postgres), then
   `rm -f ~/.paperclip/instances/default/db/postmaster.pid` and restart.

5. **URL hangs / spinner, TLS fine, no response** — the nohup'd dev stack got
   suspended reading the tty (`ps` state `T`/`TN`; happens when started
   without `< /dev/null`). Suspended processes keep ports open but never
   answer. `kill -9` the whole suspended tree (node processes only — safe),
   restart with the canonical nohup line.

6. **Connection hangs before TLS / VIP unreachable** — Tailscale serve not
   routing: device advertisement or service host approval missing. Re-run the
   relevant `tailscale serve ...` line (device or service), then check the
   admin console Services page shows the host Online/approved. Note
   `tailscale serve status` output may omit the service block; trust the admin
   console.

7. **After reboot nothing works** — all three halves are currently manual: the
   nohup'd server dies with the machine and both serve advertisements may not
   persist. Re-run the start line and both `tailscale serve` lines (device +
   service). `paperclip-up.sh` does all three.

8. **Adapter env check fails: `Python 3.9.6 found — requires 3.10+`** — the
   *server process's* inherited PATH resolves `python3` to macOS system Python,
   even though Hermes itself runs on its own 3.11. Hermes's bundled Python does
   not count; the probe runs bare `python3 --version`. Fix: `brew install python`
   so `/opt/homebrew/bin/python3` (3.10+) is first on PATH, then **restart the
   server from that same terminal** so it inherits the PATH (the running process
   keeps its old PATH forever). `paperclip-up.sh` prepends the Homebrew bin dir
   for this reason.

9. **Endless port roulette / server keeps landing on 3101 / `pnpm dev` boots a
   `/tmp/pcvt-*/vt-*` test instance** — ROOT CAUSE of the 2026-07-03 night-long
   fight. Each `pnpm dev` whose parent was killed left an **orphaned file
   watcher** (`tsx ... watch ... src/index.ts`, reparented to PPID 1) that
   respawned a server. Combined with the dev-runner's idempotency ("if a dev
   runner is already alive, report it instead of starting a new one"), every new
   `pnpm dev` *deferred to a leftover orphan* — including a stray `vt-*` test
   instance from an agent's test run — instead of starting the real default
   instance. Symptom: 3100 empty, an unkillable-looking server reappearing on
   3101, health showing `local_trusted` or a `/tmp` backup dir. Fix: kill the
   whole forest, then start exactly one pinned server:

   ```sh
   pkill -f "tsx/dist/cli.mjs watch.*src/index.ts"
   pkill -f "dev-runner.ts"; pkill -f "dev-watch.ts"; pkill -f "@paperclipai/server dev:watch"
   lsof -tnP -iTCP:3100 -sTCP:LISTEN | xargs -r kill
   lsof -tnP -iTCP:3101 -sTCP:LISTEN | xargs -r kill
   # confirm zero, then:
   PAPERCLIP_HOME="$HOME/.paperclip" PAPERCLIP_INSTANCE_ID=default \
     nohup pnpm dev --authenticated-private --bind loopback < /dev/null > ~/paperclip-dev.log 2>&1 &
   ```

   Diagnose leftovers with:
   `ps -A -o pid,ppid,etime,command | grep -E "tsx.*src/index.ts|dev-runner|dev-watch" | grep -v grep`
   — orphans show `PPID 1`. `paperclip-up.sh` automates this.

10. **Governance: agent editing the live checkout** — the `vt-*` test instance
    and stray watchers traced back to a Paperclip **agent operating inside
    `~/paperclip` itself** (commits `HOM-13`/`HOM-9`, `server/Day`,
    `server/ui-dist/`, running the test suite which spun up `/tmp` Postgres +
    servers on 3101). An agent must NOT have the live Paperclip checkout as its
    workspace — it fights the platform hosting it. Fix: pause the agent, unassign
    its in-progress `HOM-*` issues, and give it an isolated project workspace
    (git worktree / operator branch) instead of `~/paperclip`. Decide whether to
    keep, upstream, or `git reset --hard origin/master` its commits.

11. **Board claim / "No company access" after sign-in** — account exists but
   isn't instance admin. Get the newest one-time claim URL:
   `grep -o 'board-claim/[^ ]*' ~/paperclip-dev.log | tail -1` and open it as
   `https://paperclip.tail195627.ts.net/board-claim/<token>?code=<code>`
   while signed in. Each boot mints a new token; only the latest is valid.

## Hermes gateway (optional second adapter mode)

For `hermes_gateway` agents (Hermes as a persistent API server instead of
per-heartbeat CLI):

```sh
API_SERVER_ENABLED=true API_SERVER_KEY="$(openssl rand -hex 32)" \
  hermes gateway run --accept-hooks        # port 8642; requires aiohttp installed
```

Gotchas: `API_SERVER_KEY` must be ≥16 chars or Hermes refuses to start the
API server; `aiohttp` is not installed by `pip install hermes-agent` alone.
Agent config in Paperclip: `apiBaseUrl: http://127.0.0.1:8642`, `apiKey` =
the gateway key, `paperclipApiUrl: http://127.0.0.1:3100`.
