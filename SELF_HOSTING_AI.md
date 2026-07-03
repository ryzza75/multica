# Self-Hosting Setup (for AI Agents)

This document is designed for AI agents to execute. Follow these steps exactly to deploy a local Multica instance and connect to it.

## Prerequisites

- Docker and Docker Compose installed
- Homebrew installed (for CLI)
- At least one AI agent CLI on PATH: `claude` or `codex`

## Install

```bash
# Install CLI + provision self-host server
curl -fsSL https://raw.githubusercontent.com/multica-ai/multica/main/scripts/install.sh | bash -s -- --with-server

# Configure CLI for localhost, authenticate, and start daemon
multica setup self-host
```

Wait for the server output `✓ Multica server is running and CLI is ready!` before running `multica setup self-host`.

**Expected result:**
- Frontend at http://localhost:3000
- Backend at http://localhost:8080
- `multica` CLI installed and configured for localhost

## Alternative: Manual Setup

```bash
git clone https://github.com/multica-ai/multica.git
cd multica
make selfhost
brew install multica-ai/tap/multica
multica setup self-host
```

The `multica setup self-host` command will:
1. Configure CLI to connect to localhost:8080 / localhost:3000
2. Open a browser for login — use the emailed code, or the generated code printed in backend logs when Resend is unset
3. Discover workspaces automatically
4. Start the daemon in the background

## Verification

```bash
multica daemon status
```

Should show `running` with detected agents.

## Stopping

```bash
# Stop the daemon
multica daemon stop

# Stop all Docker services
cd multica
make selfhost-stop
```

## Custom Ports

If the default ports (8080/3000) are in use:

1. Edit `.env` and change `PORT` and `FRONTEND_PORT`
2. Run `make selfhost`
3. Run `multica setup self-host --port <PORT> --frontend-port <FRONTEND_PORT>`

## Knowledge Graph Semantic Search (optional)

The workspace knowledge graph (`multica knowledge`) gains a semantic search
arm when an embedding provider is configured. Without it, knowledge search
is lexical-only — nothing else changes.

Set these on the backend:

```bash
MULTICA_EMBEDDING_API_KEY=sk-...             # bearer token (optional for local providers)
MULTICA_EMBEDDING_API_BASE=https://api.openai.com/v1   # any OpenAI-compatible /embeddings endpoint
MULTICA_EMBEDDING_MODEL=text-embedding-3-small          # must produce 1536-dim vectors
```

Requirements and behavior:

- The database needs the pgvector extension. The bundled
  `pgvector/pgvector:pg17` image ships it; migration 129 enables it
  automatically and skips gracefully when it is unavailable.
- A background job (`knowledge_embedding_backfill`, every 5 minutes) embeds
  new and edited knowledge nodes; changing the model re-embeds everything
  incrementally.
- The configured model must emit 1536-dimension vectors (the OpenAI
  `text-embedding-3-*` family is requested at that size automatically).
  A mismatched model fails loudly in the job log rather than writing bad
  vectors.
- Local keyless providers (Ollama, vLLM) work by setting only
  `MULTICA_EMBEDDING_API_BASE`.

## Troubleshooting

- **Backend not ready:** `docker compose -f docker-compose.selfhost.yml logs backend`
- **Frontend not ready:** `docker compose -f docker-compose.selfhost.yml logs frontend`
- **Daemon issues:** `multica daemon logs`
- **Health checks:** `curl http://localhost:8080/health` for liveness, `curl http://localhost:8080/readyz` for dependency-aware readiness
