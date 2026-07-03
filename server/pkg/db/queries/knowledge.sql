-- name: CreateKnowledgeNode :one
INSERT INTO knowledge_node (
    workspace_id, kind, slug, title, aliases, summary, content, attrs, status,
    created_by_type, created_by_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: GetKnowledgeNodeInWorkspace :one
SELECT * FROM knowledge_node
WHERE id = $1 AND workspace_id = $2;

-- name: GetKnowledgeNodeBySlug :one
SELECT * FROM knowledge_node
WHERE workspace_id = $1 AND slug = $2;

-- name: ListKnowledgeNodes :many
SELECT * FROM knowledge_node
WHERE workspace_id = $1
  AND merged_into IS NULL
  AND (sqlc.narg('kind')::text IS NULL OR kind = sqlc.narg('kind'))
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
ORDER BY updated_at DESC
LIMIT $2 OFFSET $3;

-- name: SearchKnowledgeNodeCandidates :many
-- Dedup-first search: exact slug, title substring, or alias match. Used both
-- by GET /search (lexical arm) and by the create-flow duplicate check.
SELECT * FROM knowledge_node
WHERE workspace_id = $1
  AND merged_into IS NULL
  AND (
    slug = lower(sqlc.arg('query')::text)
    OR title ILIKE '%' || sqlc.arg('query')::text || '%'
    OR EXISTS (
      SELECT 1 FROM jsonb_array_elements_text(aliases) AS a
      WHERE lower(a) = lower(sqlc.arg('query')::text)
    )
  )
ORDER BY (slug = lower(sqlc.arg('query')::text)) DESC, updated_at DESC
LIMIT $2;

-- name: ListKnowledgeNodesByIDs :many
SELECT * FROM knowledge_node
WHERE workspace_id = $1 AND id = ANY(sqlc.arg('ids')::uuid[]);

-- name: UpdateKnowledgeNode :one
UPDATE knowledge_node
SET title      = $3,
    summary    = $4,
    content    = $5,
    aliases    = $6,
    attrs      = $7,
    status     = $8,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: MarkKnowledgeNodeMerged :one
UPDATE knowledge_node
SET merged_into = $3,
    updated_at  = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteKnowledgeNode :exec
DELETE FROM knowledge_node
WHERE id = $1 AND workspace_id = $2;

-- name: CreateKnowledgeSource :one
INSERT INTO knowledge_source (
    workspace_id, source_type, source_ref, title, content,
    created_by_type, created_by_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: GetKnowledgeSourceInWorkspace :one
SELECT * FROM knowledge_source
WHERE id = $1 AND workspace_id = $2;

-- name: GetKnowledgeSourceByURL :one
SELECT * FROM knowledge_source
WHERE workspace_id = $1
  AND source_type = 'url'
  AND source_ref->>'url' = sqlc.arg('url')::text
ORDER BY created_at ASC
LIMIT 1;

-- name: CreateKnowledgeEdge :one
INSERT INTO knowledge_edge (
    workspace_id, src_type, src_id, dst_type, dst_id, predicate,
    confidence, attrs, status, valid_from, created_by_type, created_by_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
) RETURNING *;

-- name: GetKnowledgeEdgeInWorkspace :one
SELECT * FROM knowledge_edge
WHERE id = $1 AND workspace_id = $2;

-- name: GetLiveKnowledgeEdgeByEndpoints :one
SELECT * FROM knowledge_edge
WHERE workspace_id = $1
  AND src_type = $2 AND src_id = $3
  AND dst_type = $4 AND dst_id = $5
  AND predicate = $6
  AND valid_until IS NULL;

-- name: ListLiveKnowledgeEdgesFromSource :many
-- All live edges leaving (src, predicate). Used for functional-predicate
-- conflict detection: a functional predicate may have at most one live edge
-- per source endpoint.
SELECT * FROM knowledge_edge
WHERE workspace_id = $1
  AND src_type = $2 AND src_id = $3
  AND predicate = $4
  AND valid_until IS NULL;

-- name: ListKnowledgeEdgesTouching :many
-- Live edges touching a single endpoint, for GET /edges and merge flows.
SELECT * FROM knowledge_edge
WHERE workspace_id = $1
  AND ((src_type = $2 AND src_id = $3) OR (dst_type = $2 AND dst_id = $3))
  AND (sqlc.narg('predicate')::text IS NULL OR predicate = sqlc.narg('predicate'))
  AND (sqlc.arg('include_closed')::bool OR valid_until IS NULL)
ORDER BY confidence DESC, created_at DESC
LIMIT $4;

-- name: ListKnowledgeEdgesForFrontier :many
-- One hop of graph expansion: every edge touching any endpoint of the given
-- type in the id set. Ordered by confidence so the per-hop cap keeps the
-- strongest edges. as_of renders the graph at a point in time: NULL means
-- "live now" (valid_until IS NULL); non-NULL keeps edges whose validity
-- window contains the instant.
SELECT * FROM knowledge_edge
WHERE workspace_id = $1
  AND ((src_type = $2 AND src_id = ANY(sqlc.arg('ids')::uuid[]))
    OR (dst_type = $2 AND dst_id = ANY(sqlc.arg('ids')::uuid[])))
  AND status = ANY(sqlc.arg('statuses')::text[])
  AND (
    (sqlc.narg('as_of')::timestamptz IS NULL AND valid_until IS NULL)
    OR (sqlc.narg('as_of')::timestamptz IS NOT NULL
        AND (valid_from IS NULL OR valid_from <= sqlc.narg('as_of'))
        AND (valid_until IS NULL OR valid_until > sqlc.narg('as_of')))
  )
  AND confidence >= sqlc.arg('min_confidence')::real
ORDER BY confidence DESC, created_at DESC
LIMIT $3;

-- name: AffirmKnowledgeEdge :one
UPDATE knowledge_edge
SET last_affirmed_at = now(),
    confidence       = $3
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateKnowledgeEdgeStatus :one
UPDATE knowledge_edge
SET status = $3
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CloseKnowledgeEdge :one
UPDATE knowledge_edge
SET valid_until   = now(),
    superseded_by = sqlc.narg('superseded_by')
WHERE id = $1 AND workspace_id = $2 AND valid_until IS NULL
RETURNING *;

-- name: UpdateKnowledgeEdgeEndpoints :one
-- Merge support: re-point one side of an edge at the surviving node.
UPDATE knowledge_edge
SET src_type = $3, src_id = $4, dst_type = $5, dst_id = $6
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteKnowledgeEdge :exec
DELETE FROM knowledge_edge
WHERE id = $1 AND workspace_id = $2;

-- name: CreateKnowledgeEvidence :one
INSERT INTO knowledge_evidence (
    workspace_id, edge_id, source_id, stance, note, created_by_type, created_by_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (edge_id, source_id, stance) DO UPDATE SET note = EXCLUDED.note
RETURNING *;

-- name: ListKnowledgeEvidenceForEdge :many
SELECT sqlc.embed(knowledge_evidence), sqlc.embed(knowledge_source)
FROM knowledge_evidence
JOIN knowledge_source ON knowledge_source.id = knowledge_evidence.source_id
WHERE knowledge_evidence.edge_id = $1 AND knowledge_evidence.workspace_id = $2
ORDER BY knowledge_evidence.created_at DESC;

-- name: ListKnowledgeEvidenceStances :many
SELECT stance, created_at FROM knowledge_evidence
WHERE edge_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: CreateKnowledgeNodeRevision :one
INSERT INTO knowledge_node_revision (
    workspace_id, node_id, content, summary, edited_by_type, edited_by_id
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: ListKnowledgeNodeRevisions :many
SELECT * FROM knowledge_node_revision
WHERE node_id = $1 AND workspace_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: DeleteKnowledgeEdgesForEndpoint :exec
-- Node-deletion cleanup: polymorphic endpoints cannot carry FKs, so edges
-- touching a deleted node are removed explicitly.
DELETE FROM knowledge_edge
WHERE workspace_id = $1
  AND ((src_type = $2 AND src_id = $3) OR (dst_type = $2 AND dst_id = $3));

-- name: ListKnowledgeEdgesByStatus :many
-- Review-queue listing: live edges in a given status across the workspace
-- (proposed = awaiting human review).
SELECT * FROM knowledge_edge
WHERE workspace_id = $1
  AND status = $2
  AND valid_until IS NULL
ORDER BY created_at DESC
LIMIT $3;
