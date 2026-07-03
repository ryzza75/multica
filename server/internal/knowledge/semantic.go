// Package knowledge implements the semantic (RAG) arm of the workspace
// knowledge graph: an OpenAI-compatible embedding client, a pgvector-backed
// store, reciprocal-rank fusion for hybrid search, and the scheduler job
// that keeps node embeddings fresh.
//
// Everything degrades gracefully: with no embedding provider configured, or
// on a Postgres without pgvector (knowledge_embedding absent — see
// migration 129), SemanticSearch returns ErrSemanticUnavailable and callers
// fall back to lexical search.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// EmbeddingDim is baked into the knowledge_embedding column type
// (vector(1536), migration 129). The client validates every provider
// response against it so a misconfigured model fails loudly instead of
// writing garbage.
const EmbeddingDim = 1536

// ErrSemanticUnavailable means no provider is configured or the database
// has no pgvector support. Callers treat it as "use lexical only".
var ErrSemanticUnavailable = errors.New("knowledge semantic search unavailable")

// Executor is the subset of pgxpool.Pool the semantic store needs. It
// matches the handler package's dbExecutor so the same pool flows through.
type Executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Config is the embedding-provider configuration, read from the
// environment:
//
//	MULTICA_EMBEDDING_API_BASE  OpenAI-compatible base URL
//	                            (default https://api.openai.com/v1; set to a
//	                            local server such as Ollama/vLLM for keyless)
//	MULTICA_EMBEDDING_API_KEY   bearer token; optional for local providers
//	MULTICA_EMBEDDING_MODEL     model id (default text-embedding-3-small)
//
// Semantic search is enabled when either an API key or an explicit base URL
// is present. The configured model MUST produce 1536-dim vectors (the
// OpenAI text-embedding-3-* family accepts a dimensions parameter, which
// the client sends automatically).
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func ConfigFromEnv() Config {
	cfg := Config{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_EMBEDDING_API_BASE")), "/"),
		APIKey:  strings.TrimSpace(os.Getenv("MULTICA_EMBEDDING_API_KEY")),
		Model:   strings.TrimSpace(os.Getenv("MULTICA_EMBEDDING_MODEL")),
		Timeout: 30 * time.Second,
	}
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}
	return cfg
}

// Enabled reports whether an embedding provider is configured at all.
func (c Config) Enabled() bool {
	return c.APIKey != "" || c.BaseURL != ""
}

func (c Config) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.openai.com/v1"
}

// Service bundles the embedding client and the vector store.
type Service struct {
	cfg  Config
	pool Executor
	http *http.Client

	availMu    sync.Mutex
	availKnown bool
	avail      bool
}

func New(cfg Config, pool Executor) *Service {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Service{cfg: cfg, pool: pool, http: &http.Client{Timeout: timeout}}
}

func NewFromEnv(pool Executor) *Service {
	return New(ConfigFromEnv(), pool)
}

func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enabled()
}

// VectorAvailable reports whether the knowledge_embedding table exists,
// i.e. migration 129 found pgvector. Cached after the first check: the
// answer only changes across migrations, which restart the server.
func (s *Service) VectorAvailable(ctx context.Context) bool {
	if s == nil || s.pool == nil {
		return false
	}
	s.availMu.Lock()
	defer s.availMu.Unlock()
	if s.availKnown {
		return s.avail
	}
	var regclass *string
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('knowledge_embedding')::text`).Scan(&regclass); err != nil {
		// Transient DB error — report unavailable but do not cache, so a
		// recovered connection gets a fresh answer.
		return false
	}
	s.availKnown = true
	s.avail = regclass != nil
	return s.avail
}

// ── Embedding client (OpenAI-compatible /embeddings) ──

type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed returns one vector per input, in input order. Fails closed on
// dimension mismatch so a misconfigured model can never poison the store.
func (s *Service) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if !s.Enabled() {
		return nil, ErrSemanticUnavailable
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	reqBody := embeddingRequest{Model: s.cfg.Model, Input: inputs}
	// The OpenAI text-embedding-3 family supports requesting a specific
	// dimensionality; other providers may reject the unknown field, so it
	// is only sent where it is known to be understood.
	if strings.HasPrefix(s.cfg.Model, "text-embedding-3") {
		reqBody.Dimensions = EmbeddingDim
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.baseURL()+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("embedding response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding provider returned %d: %s", resp.StatusCode, truncateForError(body))
	}
	var parsed embeddingResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(parsed.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(parsed.Data), len(inputs))
	}
	out := make([][]float32, len(inputs))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(inputs) {
			return nil, fmt.Errorf("embedding provider returned out-of-range index %d", d.Index)
		}
		if len(d.Embedding) != EmbeddingDim {
			return nil, fmt.Errorf("model %q produced %d-dim vectors; knowledge_embedding requires %d — configure a 1536-dim model",
				s.cfg.Model, len(d.Embedding), EmbeddingDim)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embedding provider returned no vector for input %d", i)
		}
	}
	return out, nil
}

func truncateForError(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// ── Vector store ──

// vectorLiteral renders a pgvector input literal ('[0.1,0.2,...]').
func vectorLiteral(vec []float32) string {
	var b strings.Builder
	b.Grow(len(vec) * 10)
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", v)
	}
	b.WriteByte(']')
	return b.String()
}

// UpsertNodeEmbedding writes/refreshes one node's vector.
func (s *Service) UpsertNodeEmbedding(ctx context.Context, nodeID, workspaceID string, vec []float32) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO knowledge_embedding (node_id, workspace_id, model, embedding, updated_at)
		VALUES ($1, $2, $3, $4::vector, now())
		ON CONFLICT (node_id) DO UPDATE
		SET model = EXCLUDED.model, embedding = EXCLUDED.embedding, updated_at = now()
	`, nodeID, workspaceID, s.cfg.Model, vectorLiteral(vec))
	return err
}

// SemanticSearch embeds the query and returns node IDs by cosine
// proximity, best first. Returns ErrSemanticUnavailable when the provider
// or pgvector is absent so callers fall back to lexical-only.
func (s *Service) SemanticSearch(ctx context.Context, workspaceID, query string, limit int) ([]string, error) {
	if !s.Enabled() || !s.VectorAvailable(ctx) {
		return nil, ErrSemanticUnavailable
	}
	if limit <= 0 {
		limit = 20
	}
	vecs, err := s.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT node_id::text
		FROM knowledge_embedding
		WHERE workspace_id = $1 AND model = $2
		ORDER BY embedding <=> $3::vector
		LIMIT $4
	`, workspaceID, s.cfg.Model, vectorLiteral(vecs[0]), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ── Hybrid fusion ──

// RRFMerge fuses ranked ID lists with reciprocal-rank fusion
// (score = Σ 1/(k+rank), k=60): items ranked well by either arm float to
// the top without either arm's raw scores needing to be comparable.
func RRFMerge(rankings ...[]string) []string {
	const k = 60
	scores := map[string]float64{}
	first := map[string]int{} // stable tiebreak: earliest appearance order
	order := 0
	for _, ranking := range rankings {
		for rank, id := range ranking {
			scores[id] += 1.0 / float64(k+rank+1)
			if _, seen := first[id]; !seen {
				first[id] = order
				order++
			}
		}
	}
	out := make([]string, 0, len(scores))
	for id := range scores {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		if scores[out[i]] != scores[out[j]] {
			return scores[out[i]] > scores[out[j]]
		}
		return first[out[i]] < first[out[j]]
	})
	return out
}

// ── Backfill: which nodes need (re-)embedding ──

// staleNode is one node whose embedding is missing, older than the node's
// last update, or built with a different model.
type staleNode struct {
	ID          string
	WorkspaceID string
	Kind        string
	Title       string
	Summary     string
	Content     string
	Aliases     string
}

func (s *Service) listStaleNodes(ctx context.Context, limit int) ([]staleNode, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT n.id::text, n.workspace_id::text, n.kind, n.title,
		       COALESCE(n.summary, ''), COALESCE(n.content, ''), n.aliases::text
		FROM knowledge_node n
		LEFT JOIN knowledge_embedding e ON e.node_id = n.id
		WHERE n.merged_into IS NULL
		  AND n.status <> 'rejected'
		  AND (e.node_id IS NULL OR e.updated_at < n.updated_at OR e.model <> $1)
		ORDER BY n.updated_at ASC
		LIMIT $2
	`, s.cfg.Model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []staleNode
	for rows.Next() {
		var n staleNode
		if err := rows.Scan(&n.ID, &n.WorkspaceID, &n.Kind, &n.Title, &n.Summary, &n.Content, &n.Aliases); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// embeddingText is the canonical text a node is embedded from: identity
// first (kind/title/aliases), then the summary, then a bounded slice of
// the wiki body so enormous pages cannot blow the provider's token limit.
func (n staleNode) embeddingText() string {
	var b strings.Builder
	b.WriteString(n.Kind)
	b.WriteString(": ")
	b.WriteString(n.Title)
	if n.Aliases != "" && n.Aliases != "[]" {
		b.WriteString("\naliases: ")
		b.WriteString(n.Aliases)
	}
	if n.Summary != "" {
		b.WriteString("\n")
		b.WriteString(n.Summary)
	}
	if n.Content != "" {
		b.WriteString("\n")
		content := n.Content
		if len(content) > 6000 {
			content = content[:6000]
		}
		b.WriteString(content)
	}
	return b.String()
}

// EmbedStaleNodes embeds up to batch stale nodes and upserts their
// vectors. Returns how many nodes were embedded.
func (s *Service) EmbedStaleNodes(ctx context.Context, batch int) (int, error) {
	if !s.Enabled() || !s.VectorAvailable(ctx) {
		return 0, nil
	}
	if batch <= 0 {
		batch = 32
	}
	nodes, err := s.listStaleNodes(ctx, batch)
	if err != nil {
		return 0, fmt.Errorf("list stale nodes: %w", err)
	}
	if len(nodes) == 0 {
		return 0, nil
	}
	inputs := make([]string, len(nodes))
	for i, n := range nodes {
		inputs[i] = n.embeddingText()
	}
	vecs, err := s.Embed(ctx, inputs)
	if err != nil {
		return 0, fmt.Errorf("embed batch: %w", err)
	}
	embedded := 0
	for i, n := range nodes {
		if err := s.UpsertNodeEmbedding(ctx, n.ID, n.WorkspaceID, vecs[i]); err != nil {
			return embedded, fmt.Errorf("upsert embedding for node %s: %w", n.ID, err)
		}
		embedded++
	}
	return embedded, nil
}
