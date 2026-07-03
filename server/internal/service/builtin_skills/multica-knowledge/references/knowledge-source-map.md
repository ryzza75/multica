# Knowledge graph source map

Where each behavior documented in SKILL.md lives in the codebase. Update this
file and SKILL.md together when changing the CLI or API surface (CLAUDE.md
rule).

## Plan / design

- `docs/knowledge-graph-plan.md` — the full RFC: ontology, fact lifecycle
  (evidence, affirmation, staleness, supersedence), memory intake pipeline,
  runtime-native memory policy, delivery phases.

## Schema

- `server/migrations/128_knowledge_graph.up.sql` — `knowledge_node`,
  `knowledge_edge` (temporal validity + live-only partial unique index),
  `knowledge_source`, `knowledge_evidence`, `knowledge_node_revision`.
- Embedding columns are deliberately absent until the RAG phase.

## Server

- Vocabulary (kinds, predicates, functional predicates, endpoint types,
  source types, statuses): `server/internal/handler/knowledge.go`
  (`knowledgeNodeKinds`, `knowledgePredicates`, `knowledgeEndpointTypes`,
  `knowledgeSourceTypes`, `knowledgeStatuses`).
- Trust gate (member → confirmed, agent → proposed):
  `gateKnowledgeStatus` in `server/internal/handler/knowledge.go`.
- Dedup-first create (409 + candidates unless `confirm_new`):
  `CreateKnowledgeNode` in `server/internal/handler/knowledge.go`.
- Reconciled edge create (affirm identical live facts, functional-conflict
  handling, member-only supersede): `CreateKnowledgeEdge` in
  `server/internal/handler/knowledge_edge.go`.
- Evidence + confidence recompute: `attachEvidenceAndRecompute` and
  `recomputeKnowledgeConfidence` in
  `server/internal/handler/knowledge_edge.go`.
- Graph expansion and path finding (bounded BFS, `as_of` time travel):
  `server/internal/handler/knowledge_graph.go`.
- Member-only guards (close, delete, merge, status changes): enforced in
  the respective handlers via `resolveActor` returning `agent`.
- Routes: `server/cmd/server/router.go` under `/api/knowledge`.
- Realtime events: `knowledge_node:*`, `knowledge_edge:*` in
  `server/pkg/protocol/events.go`.
- Queries: `server/pkg/db/queries/knowledge.sql` (sqlc).

## Semantic search (RAG)

- Embedding client, vector store, RRF fusion, staleness scan:
  `server/internal/knowledge/semantic.go`. Provider config comes from
  `MULTICA_EMBEDDING_API_BASE` / `MULTICA_EMBEDDING_API_KEY` /
  `MULTICA_EMBEDDING_MODEL` (OpenAI-compatible `/embeddings`; model must
  emit 1536-dim vectors).
- Backfill scheduler job (`knowledge_embedding_backfill`, 5m cadence):
  `server/internal/knowledge/job.go`, registered in
  `server/cmd/server/main.go`.
- Vector schema (guarded — skipped without pgvector):
  `server/migrations/129_knowledge_embeddings.up.sql`
  (`knowledge_embedding`, HNSW cosine index).
- Hybrid search handler (lexical + semantic, RRF-fused, `"semantic"`
  response flag, silent lexical fallback on provider failure):
  `SearchKnowledgeNodes` in `server/internal/handler/knowledge.go`.

## CLI

- `server/cmd/multica/cmd_knowledge.go` — `multica knowledge search`,
  `node add|get|list`, `edge add|close`, `graph`, `path`,
  `review list|approve|reject`. Registered in
  `server/cmd/multica/main.go`.

## Behavioral contracts worth re-checking in code

- Affirmation: `POST /api/knowledge/edges` returns HTTP 200 with
  `"affirmed": true` (not 201) when the identical live fact already
  exists; evidence is attached when provenance was supplied.
- Functional conflict, agent actor: edge is created as `proposed`, the
  live fact stays open, response carries `conflicts_pending_review`.
- Functional conflict, member actor without `supersede`: HTTP 409 with
  `conflicts`.
- Merge redirects: `GET /api/knowledge/nodes/{id}` follows `merged_into`
  so pre-merge references resolve to the surviving node.
- Review queue: `GET /api/knowledge/edges?status=proposed` (no endpoint
  filter) and `GET /api/knowledge/nodes?status=proposed`.
