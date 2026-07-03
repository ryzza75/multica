-- Knowledge semantic search (RAG phase of docs/knowledge-graph-plan.md).
--
-- pgvector is present in every first-party Postgres image (CI, compose,
-- self-host all run pgvector/pgvector:pg17), but a self-hosted external
-- Postgres may lack it. Mirror the pg_bigm pattern from
-- 032_issue_search_index: try to enable the extension, and skip the
-- vector-typed objects gracefully when it is unavailable. Everything that
-- reads knowledge_embedding checks for the table first, so a database
-- without pgvector simply has no semantic search arm.
--
-- The embedding column is intentionally in its own table (not on
-- knowledge_node): sqlc parses migrations as schema and has no mapping for
-- the vector type, and this keeps the node model clean on databases where
-- the extension is missing.

DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;
EXCEPTION
    WHEN OTHERS THEN
        RAISE NOTICE 'pgvector extension unavailable, knowledge semantic search disabled: %', SQLERRM;
END
$$;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        -- 1536 dims = OpenAI text-embedding-3-small (the default model).
        -- Other models must be configured to emit 1536 dims; the embed
        -- client validates response dimensions before writing.
        CREATE TABLE IF NOT EXISTS knowledge_embedding (
            node_id      UUID PRIMARY KEY REFERENCES knowledge_node(id) ON DELETE CASCADE,
            workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
            model        TEXT NOT NULL,
            embedding    vector(1536) NOT NULL,
            updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
        );
        CREATE INDEX IF NOT EXISTS idx_knowledge_embedding_ws
            ON knowledge_embedding (workspace_id);
        -- HNSW over cosine distance; small default build params are fine at
        -- workspace scale (tens of thousands of nodes).
        CREATE INDEX IF NOT EXISTS idx_knowledge_embedding_hnsw
            ON knowledge_embedding USING hnsw (embedding vector_cosine_ops);
    END IF;
END
$$;
