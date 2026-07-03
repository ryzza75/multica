# Knowledge Graph & Insight Discovery — Development Plan

Status: proposal / RFC
Scope: server, packages/core, packages/views, apps/web, apps/desktop, CLI, builtin skills

## 1. Goal

Turn the knowledge that accumulates in a Multica workspace — from issues, agent
task output, research, documents, and deliberate note-taking — into a typed,
provenance-tracked knowledge graph that can be:

1. **Queried multi-hop** ("how is author X connected to market trend Y?"),
2. **Visualized** as an interactive node/edge explorer (web + desktop),
3. **Read and written by agents** (Hermes, Claude, Kimi, Kiro — any runtime),
4. **Retrieved semantically** (RAG over node content and source documents),
5. **Kept organised, standardised, and complete** via an explicit ontology,
   dedup rules, mandatory provenance, and a review/curation loop.

Node kinds are open-ended by design: people, organizations, events, concepts,
ideas, claims/facts, works (books/papers/articles), brands, markets,
technologies, places — plus Multica's own entities (issues, projects, agents,
members) so work items and knowledge live in one connected graph.

## 2. Current state (audited)

What exists today and what does not — this plan builds on the former and
creates the latter:

| Capability | State |
| --- | --- |
| Wiki / document store | **Does not exist.** `project_resource` is a typed JSONB pointer (`github_repo`, `local_directory`) with no content body. No `document`/`page` table anywhere in `server/migrations/`. |
| RAG / embeddings | **Does not exist.** No `CREATE EXTENSION vector`, no embedding columns. Search is pg_bigm + `LIKE` (`032_issue_search_index.up.sql`, `buildProjectSearchQuery`). |
| pgvector availability | **Provisioned everywhere but unused**: CI (`.github/workflows/ci.yml`), `docker-compose.yml`, `docker-compose.selfhost.yml` all run `pgvector/pgvector:pg17`. Enabling the extension is a migration away. |
| Graph edges | Bespoke per-pair tables only. `issue_dependency` (`blocks`/`blocked_by`/`related`) is the closest existing typed edge. No generic edge table, no resource↔resource or resource↔issue links. |
| Graph visualization | **No dependency exists** (no d3/cytoscape/sigma/react-flow anywhere). |
| Agent context injection | Exists and is the key integration seam: `server/internal/daemon/execenv/context.go` writes `.agent_context/issue_context.md`, provider-native skills, and `.multica/project/resources.json` into every task's working directory. |
| Agent write path | Exists: the `multica` CLI + builtin skills (`server/internal/service/builtin_skills/*`) teach every runtime, including Hermes (ACP backend, `server/pkg/agent/hermes.go`), how to mutate durable workspace state. |

Conclusion: the knowledge substrate is greenfield, but every integration seam
it needs (polymorphic actor pattern, CLI-as-agent-API, skill injection, sidecar
context files, sqlc/chi/TanStack pipeline) already exists and should be reused
verbatim.

## 3. Architecture decision

**One substrate, three views over it.** The knowledge base, the wiki, and the
RAG corpus are not three systems — they are three access patterns over the
same two tables:

- A **node** (`knowledge_node`) is simultaneously a graph vertex, a wiki page
  (its `content` is markdown), and a RAG chunk source (its embedding).
- An **edge** (`knowledge_edge`) is a typed, directed, provenance-carrying
  relationship. Edge endpoints are polymorphic `(type, id)` pairs — matching
  the pervasive Multica pattern (`assignee_type`/`assignee_id`) — so edges can
  connect knowledge nodes to each other **and** to internal entities (issues,
  projects, agents, members) without mirroring those entities into the graph.
- A **source** (`knowledge_source`) is the provenance anchor: the URL,
  document, comment, or task output a fact came from, chunked and embedded
  for retrieval.

Everything is workspace-scoped (`workspace_id` on every table, every query
filtered by it, `X-Workspace-ID` selects it), consistent with the domain rule.

Graph queries (N-hop neighborhoods, paths) run server-side as recursive CTEs
with hop and fan-out limits. Postgres is sufficient for the target scale
(tens of thousands of nodes per workspace); no separate graph database.

## 4. Ontology and standards

This is what makes the graph "organised, standardised, complete" instead of
sludge. It ships as code (enums + validation) and as a builtin skill so agents
follow it.

### 4.1 Node kinds (initial set)

`person`, `organization`, `brand`, `concept`, `idea`, `claim`, `event`,
`work` (book/paper/article/talk), `technology`, `market`, `place`, `note`.

Kinds are a server-validated enum with a `default` branch client-side (per the
API-compatibility rule: server-driven enums always need a `default`). Each
kind defines expected `attrs` keys (e.g. `person`: `org`, `role`, `links`;
`work`: `author`, `year`, `url`, `isbn`) — validated leniently (unknown keys
allowed, known keys type-checked).

### 4.2 Canonical predicates (initial set)

Directed, snake_case, small and closed to start:

- Structure: `part_of`, `instance_of`, `related_to`
- People/orgs: `works_at`, `founded`, `member_of`, `knows`, `advised_by`
- Ideas/works: `authored`, `cites`, `supports`, `contradicts`, `influenced`,
  `derived_from`
- Events: `occurred_at`, `participated_in`, `caused`
- Work items: `mentions`, `evidence_for`, `produced_by` (issue/task → node)

New predicates require adding to the enum — deliberate friction that keeps the
edge vocabulary queryable. Free-text nuance goes in `edge.attrs.note`.

### 4.3 Identity and dedup rules

- Every node has a workspace-unique `slug` (kebab-case of canonical title) and
  an `aliases` JSONB array. **Create flow is search-first**: the API's create
  endpoint returns `409` with candidate matches (slug/alias/trigram + vector
  similarity) unless `confirm_new=true` is passed. The CLI and the skill
  encode this: search, then create.
- External identifiers live in `attrs.external_ids` (`url`, `isbn`, `doi`,
  `orcid`, `domain`) and participate in dedup matching.
- A `merge` endpoint re-points edges and folds aliases; merges are recorded so
  old IDs resolve (redirect row, not hard delete).

### 4.4 Provenance and confidence

- Every edge carries `source_id` (FK → `knowledge_source`, nullable only for
  human-asserted edges, which instead carry the asserting actor) and a
  `confidence` float (0–1).
- Every node/edge records `created_by_type`/`created_by_id`
  (`member`/`agent`) — same polymorphic actor pattern as `issue.creator_type`.

### 4.5 Curation states

Agent-extracted material lands as `status='proposed'`; humans (or a trusted
curator agent) promote to `confirmed` or reject. The graph view filters to
`confirmed` by default with a toggle to show proposed material. This is the
quality gate that keeps automated extraction from polluting the graph.

## 5. Schema (migration `128_knowledge_graph`)

```sql
CREATE EXTENSION IF NOT EXISTS vector;  -- pgvector image ships it everywhere

CREATE TABLE knowledge_node (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,          -- validated enum, see 4.1
    slug            TEXT NOT NULL,
    title           TEXT NOT NULL,
    aliases         JSONB NOT NULL DEFAULT '[]',
    summary         TEXT,                   -- one-liner for graph tooltips
    content         TEXT,                   -- markdown body = the wiki page
    attrs           JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'confirmed'
                    CHECK (status IN ('proposed','confirmed','rejected')),
    embedding       vector(1536),           -- nullable until embed job runs
    embedding_model TEXT,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member','agent')),
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

CREATE TABLE knowledge_source (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source_type     TEXT NOT NULL,          -- url | document | comment | issue | task_output | manual
    source_ref      JSONB NOT NULL,         -- {url} | {issue_id} | {comment_id} | {task_id} ...
    title           TEXT,
    content         TEXT,                   -- captured text for chunking/RAG
    created_by_type TEXT NOT NULL,
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE knowledge_edge (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    src_type        TEXT NOT NULL CHECK (src_type IN ('node','issue','project','agent','member')),
    src_id          UUID NOT NULL,
    dst_type        TEXT NOT NULL CHECK (dst_type IN ('node','issue','project','agent','member')),
    dst_id          UUID NOT NULL,
    predicate       TEXT NOT NULL,          -- validated enum, see 4.2
    confidence      REAL NOT NULL DEFAULT 1.0,
    attrs           JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'confirmed'
                    CHECK (status IN ('proposed','confirmed','rejected')),
    source_id       UUID REFERENCES knowledge_source(id) ON DELETE SET NULL,
    created_by_type TEXT NOT NULL,
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, src_type, src_id, dst_type, dst_id, predicate)
);

CREATE TABLE knowledge_chunk (               -- RAG unit for long sources
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source_id       UUID NOT NULL REFERENCES knowledge_source(id) ON DELETE CASCADE,
    position        INT NOT NULL,
    content         TEXT NOT NULL,
    embedding       vector(1536),
    embedding_model TEXT
);

CREATE INDEX idx_knowledge_edge_src ON knowledge_edge (workspace_id, src_type, src_id);
CREATE INDEX idx_knowledge_edge_dst ON knowledge_edge (workspace_id, dst_type, dst_id);
CREATE INDEX idx_knowledge_node_ws_kind ON knowledge_node (workspace_id, kind);
-- HNSW indexes added in the RAG phase, after an embedding backfill exists:
-- CREATE INDEX ... USING hnsw (embedding vector_cosine_ops);
```

Node deletion: knowledge-node endpoints handle edge cleanup for `node`-type
endpoints; internal-entity endpoints (issue/project/...) get dangling-edge
garbage collection in the read path plus a periodic sweep, since those tables
can't carry FKs into `knowledge_edge`'s polymorphic columns.

## 6. Delivery phases

Each phase is independently shippable and useful on its own.

### Phase 1 — Substrate: schema, API, CLI

Server:
- Migration `128_knowledge_graph` (+ `.down.sql`), sqlc queries in
  `server/pkg/db/queries/knowledge.sql`, `make sqlc`.
- Handlers in `server/internal/handler/knowledge.go` following
  `project_resource.go`'s shape (validation switch per kind/predicate is the
  extension seam, mirroring `validateAndNormalizeResourceRef`).
- Routes in `server/cmd/server/router.go`:

```
GET    /api/knowledge/nodes                 list (kind, status, q filters)
POST   /api/knowledge/nodes                 create (dedup-checked; 409 + candidates)
GET    /api/knowledge/nodes/{id}            get (accepts slug or UUID via loader)
PUT    /api/knowledge/nodes/{id}            update
DELETE /api/knowledge/nodes/{id}
POST   /api/knowledge/nodes/{id}/merge      merge into another node
GET    /api/knowledge/edges                 list (src/dst/predicate filters)
POST   /api/knowledge/edges                 create
DELETE /api/knowledge/edges/{id}
POST   /api/knowledge/sources               capture a source (url/text/ref)
GET    /api/knowledge/graph                 neighborhood: ?focus=&hops=&kinds=&predicates=&min_confidence=&status=&limit=
GET    /api/knowledge/path                  ?src=&dst=&max_hops=   (bidirectional BFS)
GET    /api/knowledge/search                hybrid search (Phase 3 adds vector arm)
```

- UUID handling per the backend rules: path params through a
  `loadKnowledgeNodeForUser`-style loader (accepts slug or UUID), body UUIDs
  via `parseUUIDOrBadRequest`.
- Realtime: `EventKnowledgeNodeCreated/Updated/...` in
  `server/pkg/protocol/events.go`, published via `h.publish`.

CLI (`server/cmd/multica/cmd_knowledge.go`):

```bash
multica knowledge search "<query>" --kind person --output json
multica knowledge node add --kind person --title "Jane Doe" --summary "..." --output json
multica knowledge node get <slug-or-id> --output json
multica knowledge edge add --src <node> --dst <node> --predicate influenced \
    --confidence 0.8 --source-url <url> --output json
multica knowledge link-issue <issue-id> --node <node> --predicate mentions
multica knowledge graph <node> --hops 2 --output json
multica knowledge path <node-a> <node-b> --output json
```

The graph/path CTE caps: `hops ≤ 4`, per-hop fan-out ≤ 50 (highest-confidence
first), total nodes ≤ 500 per response, and the response reports what was
truncated so callers know coverage was bounded.

### Phase 2 — Agent write path (Hermes + all runtimes)

This is deliberately runtime-agnostic: agents integrate through the `multica`
CLI and builtin skills, exactly like `multica-projects-and-resources`. Hermes
needs nothing Hermes-specific — the daemon already injects skills into every
runtime's working directory (`execenv/context.go`).

- New builtin skill `server/internal/service/builtin_skills/multica-knowledge/`
  (`SKILL.md` + `references/knowledge-source-map.md`, per the CLAUDE.md rule
  that CLI-behavior skills ship with a source map). It teaches:
  - **search-first**: always `multica knowledge search` before `node add`;
  - when to capture (novel person/org/concept/claim encountered during
    research or issue work; explicit user asks like "remember this");
  - provenance is mandatory for extracted facts (`--source-url` /
    `--source-ref`);
  - extracted material defaults to `--status proposed`; only human-instructed
    assertions go in as confirmed;
  - kind/predicate vocabulary and the attrs conventions from §4.
- **Extraction autopilot**: an autopilot (existing `autopilot` machinery) that
  runs an agent over new/updated issues, comments, and completed task outputs,
  proposing nodes/edges with provenance. Batched (e.g. daily), not per-event.
- **Curation queue**: `status='proposed'` items surface in a review list
  (Phase 4 UI) with accept/reject/merge actions; a scheduled "gardener" agent
  can also merge obvious duplicates and fill missing summaries, using the same
  CLI.

### Phase 3 — RAG layer

- Embedding worker in the server (job on node create/update and source
  chunking; model + endpoint configured like other provider settings; store
  `embedding_model` per row so re-embeds are incremental). Add HNSW indexes
  once backfill exists.
- `GET /api/knowledge/search` becomes hybrid: pg_bigm/ILIKE arm + cosine arm,
  merged with reciprocal-rank fusion. CLI: `multica knowledge search --semantic`.
- **Context injection**: extend `execenv` to write
  `.multica/knowledge/context.json` next to `resources.json` — top-K nodes
  relevant to the task brief (vector search on issue title/description), each
  with slug, summary, and top edges. The runtime brief gains a short
  `## Knowledge Context` section. This closes the loop: agents read graph
  knowledge without being asked, in every runtime including Hermes.

### Phase 4 — Graph visualization (web + desktop)

Follows the shared-feature recipe exactly (views in `packages/views`, platform
wiring per app, `useNavigation`, `wsId`-keyed queries).

- **Library: `graphology` (model + algorithms) + `sigma` (WebGL renderer)**,
  declared in `packages/views/package.json` (and `catalog:` if reused).
  Rationale: WebGL handles thousands of nodes where SVG/d3 stalls;
  graphology ships the algorithms this feature is about (shortest path,
  neighborhoods, louvain communities, betweenness). react-flow is the wrong
  tool (DAG editor, not force-directed exploration); cytoscape is viable but
  heavier and canvas-bound.
- `packages/core/knowledge/`: zod schemas parsed via `parseWithFallback`
  (mandatory — network JSON is never cast), query hooks
  (`knowledgeKeys` factory including `wsId`), mutations optimistic-by-default.
  Client-side `kind`/`predicate` handling always has a `default` branch.
- `packages/views/knowledge/`:
  - `knowledge-graph-page.tsx` — sigma canvas; focus node + N-hop expansion
    (click to expand a node's neighborhood incrementally rather than loading
    the world); filter rail (kind, predicate, confidence, status, time);
    color by kind, size by degree; community coloring toggle.
  - `node-panel.tsx` — side panel: markdown `content` (the wiki page),
    provenance list, connected issues/projects, edit affordances.
  - `path-finder.tsx` — pick two nodes → render the connecting path(s);
    this is the multi-hop "how are these related?" feature.
  - `knowledge-review.tsx` — proposed-items curation queue.
- App wiring: `apps/web/app/[slug]/knowledge/page.tsx` + desktop session
  route (tab destination, not overlay). Semantic tokens only for chrome;
  graph-specific colors defined as a small token-derived palette.
- Filter/viewport state is client state → Zustand store in `packages/core`
  (persist filters, not graph data).

### Phase 5 — Insight discovery

The "tell me something I didn't know" layer, built on Phases 1–4:

- **Weekly insight digest** (autopilot → inbox): new nodes/edges, newly
  formed communities, bridge nodes (high betweenness = concepts connecting
  otherwise-separate clusters), shortest new paths between previously
  disconnected regions.
- **Link prediction**: embedding-similar but unconnected node pairs, and
  co-citation suggestions ("A and B are cited by 4 shared sources but not
  linked") — surfaced as `proposed` edges into the curation queue, never
  auto-confirmed.
- **Question answering over the graph**: agent chat pattern — retrieve
  (hybrid search) → expand (graph neighborhood via CLI) → synthesize with
  citations to nodes/sources.

## 7. Testing and verification (per repo conventions)

- Go: handler tests for CRUD, dedup-409, merge, CTE bounds; `make test`.
- `packages/core/knowledge/*.test.ts`: schema parsing including
  **malformed-response tests** (required for every new endpoint), query-key
  scoping, optimistic rollback.
- `packages/views/knowledge/*.test.tsx`: panel/review components (no `next/*`
  or router mocks; Zustand callable-store mocks; `@multica/core/api` mocked).
  The sigma canvas itself gets a thin wrapper so tests target the data →
  graphology-model mapping, not WebGL.
- E2E: one `e2e/knowledge.spec.ts` flow (create nodes/edge via `TestApiClient`,
  open graph, expand, follow path) once Phase 4 lands.
- Docs: user docs page under `apps/docs/content/docs/` and glossary additions
  to `conventions.mdx`/`conventions.zh.mdx` for the new terms (node, edge,
  predicate, provenance) before writing any zh UI copy.

## 8. Open decisions

1. **Embedding provider/model** (Phase 3): server-side calls need a configured
   provider; dimension is baked into the column type (1536 assumed — cheap to
   change before Phase 3 ships, annoying after).
2. **Extraction aggressiveness default**: opt-in per project vs on-everything.
   Recommended: opt-in per project first; widen after the curation loop proves
   signal/noise is acceptable.
3. **Auto-linking issues↔nodes** on mention detection (like PR linking scans
   in `multica-working-on-issues`): nice, but deferred until the vocabulary
   stabilizes.
4. **Mobile**: read-only graph later, if ever — mobile is independent by
   design and out of scope here.
