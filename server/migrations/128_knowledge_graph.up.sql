-- Knowledge graph substrate (Phase 1 of docs/knowledge-graph-plan.md).
--
-- One substrate, three views: a knowledge_node is simultaneously a graph
-- vertex, a wiki page (markdown content), and a future RAG source; a
-- knowledge_edge is a typed, directed, provenance-carrying relationship
-- whose endpoints are polymorphic (type, id) pairs so edges can connect
-- knowledge nodes to each other and to internal entities (issues,
-- projects, agents, members) without mirroring those into the graph.
--
-- Embedding columns and pgvector are deliberately NOT part of this
-- migration; they arrive with the RAG phase so this schema works on any
-- Postgres regardless of available extensions.

CREATE TABLE knowledge_node (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    title           TEXT NOT NULL,
    aliases         JSONB NOT NULL DEFAULT '[]',
    summary         TEXT,
    content         TEXT,
    attrs           JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'confirmed'
                    CHECK (status IN ('proposed', 'confirmed', 'rejected')),
    -- Set when this node was merged into another; loaders resolve merged
    -- nodes transparently so old references keep working (redirect, not
    -- hard delete).
    merged_into     UUID REFERENCES knowledge_node(id) ON DELETE SET NULL,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

CREATE INDEX idx_knowledge_node_ws_kind ON knowledge_node (workspace_id, kind);

-- Provenance anchor: the URL, document, comment, issue, or task output a
-- fact came from. source_ref is intentionally polymorphic JSONB, matching
-- the project_resource.resource_ref pattern.
CREATE TABLE knowledge_source (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source_type     TEXT NOT NULL
                    CHECK (source_type IN ('url', 'document', 'comment', 'issue', 'task_output', 'runtime_memory', 'manual')),
    source_ref      JSONB NOT NULL DEFAULT '{}',
    title           TEXT,
    content         TEXT,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_knowledge_source_ws ON knowledge_source (workspace_id, created_at DESC);

CREATE TABLE knowledge_edge (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    src_type        TEXT NOT NULL
                    CHECK (src_type IN ('node', 'issue', 'project', 'agent', 'member')),
    src_id          UUID NOT NULL,
    dst_type        TEXT NOT NULL
                    CHECK (dst_type IN ('node', 'issue', 'project', 'agent', 'member')),
    dst_id          UUID NOT NULL,
    predicate       TEXT NOT NULL,
    -- Cached; recomputed from the knowledge_evidence set on evidence change.
    confidence      REAL NOT NULL DEFAULT 1.0,
    attrs           JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'confirmed'
                    CHECK (status IN ('proposed', 'confirmed', 'rejected')),
    -- Temporal validity: facts are closed (valid_until set), never deleted,
    -- so the graph can be rendered "as of" any date.
    valid_from      TIMESTAMPTZ,
    valid_until     TIMESTAMPTZ,
    superseded_by   UUID REFERENCES knowledge_edge(id) ON DELETE SET NULL,
    last_affirmed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Uniqueness applies to LIVE edges only, so a closed fact can recur later
-- (someone rejoins a company) without violating the constraint.
CREATE UNIQUE INDEX uq_knowledge_edge_live
    ON knowledge_edge (workspace_id, src_type, src_id, dst_type, dst_id, predicate)
    WHERE valid_until IS NULL;

CREATE INDEX idx_knowledge_edge_src ON knowledge_edge (workspace_id, src_type, src_id);
CREATE INDEX idx_knowledge_edge_dst ON knowledge_edge (workspace_id, dst_type, dst_id);

-- Per-source proof for an edge. An edge is a claim; each source that
-- supports or contradicts it is one evidence row. Re-encountering a known
-- fact adds evidence instead of duplicating the edge.
CREATE TABLE knowledge_evidence (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    edge_id         UUID NOT NULL REFERENCES knowledge_edge(id) ON DELETE CASCADE,
    source_id       UUID NOT NULL REFERENCES knowledge_source(id) ON DELETE CASCADE,
    stance          TEXT NOT NULL DEFAULT 'supports'
                    CHECK (stance IN ('supports', 'contradicts')),
    note            TEXT,
    created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent')),
    created_by_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (edge_id, source_id, stance)
);

CREATE INDEX idx_knowledge_evidence_edge ON knowledge_evidence (edge_id);

-- Wiki-page edit history for a node's markdown content.
CREATE TABLE knowledge_node_revision (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    node_id         UUID NOT NULL REFERENCES knowledge_node(id) ON DELETE CASCADE,
    content         TEXT,
    summary         TEXT,
    edited_by_type  TEXT NOT NULL CHECK (edited_by_type IN ('member', 'agent')),
    edited_by_id    UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_knowledge_node_revision_node ON knowledge_node_revision (node_id, created_at DESC);
