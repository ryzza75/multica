package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/knowledge"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ── Vocabulary ──
//
// Kinds and predicates are closed, code-enforced enums (see
// docs/knowledge-graph-plan.md §4). Adding one is a code change on purpose:
// a free-form vocabulary degrades into unqueryable sludge. Free-text nuance
// belongs in node/edge attrs, not in new kinds/predicates.

var knowledgeNodeKinds = map[string]bool{
	"person": true, "organization": true, "brand": true, "concept": true,
	"idea": true, "claim": true, "event": true, "work": true,
	"technology": true, "market": true, "place": true, "note": true,
}

// knowledgePredicateSpec describes server-side semantics of a predicate.
// Functional predicates admit at most one live edge per (src, predicate) —
// a new conflicting claim proposes closing the old edge instead of
// coexisting with it.
type knowledgePredicateSpec struct {
	Functional bool
}

var knowledgePredicates = map[string]knowledgePredicateSpec{
	// Structure
	"part_of": {}, "instance_of": {}, "related_to": {},
	// People / orgs
	"works_at": {Functional: true}, "founded": {}, "member_of": {},
	"knows": {}, "advised_by": {},
	// Ideas / works
	"authored": {}, "cites": {}, "supports": {}, "contradicts": {},
	"influenced": {}, "derived_from": {},
	// Events
	"occurred_at": {Functional: true}, "participated_in": {}, "caused": {},
	// Work items
	"mentions": {}, "evidence_for": {}, "produced_by": {},
}

var knowledgeEndpointTypes = map[string]bool{
	"node": true, "issue": true, "project": true, "agent": true, "member": true,
}

var knowledgeSourceTypes = map[string]bool{
	"url": true, "document": true, "comment": true, "issue": true,
	"task_output": true, "runtime_memory": true, "manual": true,
}

var knowledgeStatuses = map[string]bool{
	"proposed": true, "confirmed": true, "rejected": true,
}

// ── Responses ──

type KnowledgeNodeResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	Kind          string          `json:"kind"`
	Slug          string          `json:"slug"`
	Title         string          `json:"title"`
	Aliases       json.RawMessage `json:"aliases"`
	Summary       *string         `json:"summary"`
	Content       *string         `json:"content"`
	Attrs         json.RawMessage `json:"attrs"`
	Status        string          `json:"status"`
	MergedInto    *string         `json:"merged_into"`
	CreatedByType string          `json:"created_by_type"`
	CreatedByID   string          `json:"created_by_id"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

func knowledgeNodeToResponse(n db.KnowledgeNode) KnowledgeNodeResponse {
	aliases := json.RawMessage(n.Aliases)
	if len(aliases) == 0 {
		aliases = json.RawMessage("[]")
	}
	attrs := json.RawMessage(n.Attrs)
	if len(attrs) == 0 {
		attrs = json.RawMessage("{}")
	}
	return KnowledgeNodeResponse{
		ID:            uuidToString(n.ID),
		WorkspaceID:   uuidToString(n.WorkspaceID),
		Kind:          n.Kind,
		Slug:          n.Slug,
		Title:         n.Title,
		Aliases:       aliases,
		Summary:       textToPtr(n.Summary),
		Content:       textToPtr(n.Content),
		Attrs:         attrs,
		Status:        n.Status,
		MergedInto:    uuidToPtr(n.MergedInto),
		CreatedByType: n.CreatedByType,
		CreatedByID:   uuidToString(n.CreatedByID),
		CreatedAt:     timestampToString(n.CreatedAt),
		UpdatedAt:     timestampToString(n.UpdatedAt),
	}
}

type KnowledgeSourceResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	SourceType    string          `json:"source_type"`
	SourceRef     json.RawMessage `json:"source_ref"`
	Title         *string         `json:"title"`
	Content       *string         `json:"content"`
	CreatedByType string          `json:"created_by_type"`
	CreatedByID   string          `json:"created_by_id"`
	CreatedAt     string          `json:"created_at"`
}

func knowledgeSourceToResponse(s db.KnowledgeSource) KnowledgeSourceResponse {
	ref := json.RawMessage(s.SourceRef)
	if len(ref) == 0 {
		ref = json.RawMessage("{}")
	}
	return KnowledgeSourceResponse{
		ID:            uuidToString(s.ID),
		WorkspaceID:   uuidToString(s.WorkspaceID),
		SourceType:    s.SourceType,
		SourceRef:     ref,
		Title:         textToPtr(s.Title),
		Content:       textToPtr(s.Content),
		CreatedByType: s.CreatedByType,
		CreatedByID:   uuidToString(s.CreatedByID),
		CreatedAt:     timestampToString(s.CreatedAt),
	}
}

// ── Shared helpers ──

// resolveKnowledgeActor authenticates the request and resolves the acting
// identity as a (member|agent, uuid) pair for created_by columns. All
// knowledge writes go through this so agent-authored material is always
// attributable.
func (h *Handler) resolveKnowledgeActor(w http.ResponseWriter, r *http.Request, workspaceID string) (string, pgtype.UUID, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return "", pgtype.UUID{}, false
	}
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	actorUUID, err := util.ParseUUID(actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve actor identity")
		return "", pgtype.UUID{}, false
	}
	return actorType, actorUUID, true
}

// gateKnowledgeStatus applies the trust gate from the intake pipeline:
// members assert directly (default confirmed), agent-authored material
// always lands as proposed and is promoted by a human through the review
// flow (docs/knowledge-graph-plan.md §3.2 stage 4).
func gateKnowledgeStatus(actorType, requested string) (string, error) {
	if actorType == "agent" {
		return "proposed", nil
	}
	if requested == "" {
		return "confirmed", nil
	}
	if requested != "proposed" && requested != "confirmed" {
		return "", fmt.Errorf("invalid status %q: must be proposed or confirmed", requested)
	}
	return requested, nil
}

// knowledgeSlugify turns a title into a workspace-unique-slug candidate:
// lowercase, alphanumerics kept, everything else collapsed to single
// hyphens.
func knowledgeSlugify(title string) string {
	var b strings.Builder
	lastHyphen := true // suppress leading hyphen
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "node"
	}
	if len(slug) > 80 {
		slug = strings.Trim(slug[:80], "-")
	}
	return slug
}

// uniqueKnowledgeSlug finds a free slug in the workspace by suffixing the
// base with -2..-9, then falling back to a uuid fragment. The final INSERT
// still races under the unique constraint; callers treat a unique violation
// as a retryable conflict.
func (h *Handler) uniqueKnowledgeSlug(ctx context.Context, wsUUID pgtype.UUID, base string) (string, error) {
	candidates := []string{base}
	for i := 2; i <= 9; i++ {
		candidates = append(candidates, fmt.Sprintf("%s-%d", base, i))
	}
	for _, candidate := range candidates {
		_, err := h.Queries.GetKnowledgeNodeBySlug(ctx, db.GetKnowledgeNodeBySlugParams{
			WorkspaceID: wsUUID, Slug: candidate,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	suffix := strings.ReplaceAll(uuid.New().String(), "-", "")[:8]
	return base + "-" + suffix, nil
}

// loadKnowledgeNodeByRef resolves a node by UUID or slug, enforcing
// workspace ownership and following merge redirects so old references keep
// working after a merge.
func (h *Handler) loadKnowledgeNodeByRef(ctx context.Context, wsUUID pgtype.UUID, ref string) (db.KnowledgeNode, error) {
	var node db.KnowledgeNode
	var err error
	if id, perr := util.ParseUUID(ref); perr == nil {
		node, err = h.Queries.GetKnowledgeNodeInWorkspace(ctx, db.GetKnowledgeNodeInWorkspaceParams{
			ID: id, WorkspaceID: wsUUID,
		})
	} else {
		node, err = h.Queries.GetKnowledgeNodeBySlug(ctx, db.GetKnowledgeNodeBySlugParams{
			WorkspaceID: wsUUID, Slug: strings.ToLower(strings.TrimSpace(ref)),
		})
	}
	if err != nil {
		return db.KnowledgeNode{}, err
	}
	// Follow merge redirects (bounded — merge chains are flattened on
	// write, the bound is a corruption guard, not an expected depth).
	for i := 0; i < 5 && node.MergedInto.Valid; i++ {
		node, err = h.Queries.GetKnowledgeNodeInWorkspace(ctx, db.GetKnowledgeNodeInWorkspaceParams{
			ID: node.MergedInto, WorkspaceID: wsUUID,
		})
		if err != nil {
			return db.KnowledgeNode{}, err
		}
	}
	return node, nil
}

// loadKnowledgeNodeForUser is the handler-facing wrapper: resolves the
// workspace from the request and the node from a path param (UUID or slug),
// writing the HTTP error itself on failure.
func (h *Handler) loadKnowledgeNodeForUser(w http.ResponseWriter, r *http.Request, ref string) (db.KnowledgeNode, pgtype.UUID, bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return db.KnowledgeNode{}, pgtype.UUID{}, false
	}
	node, err := h.loadKnowledgeNodeByRef(r.Context(), wsUUID, ref)
	if err != nil {
		writeError(w, http.StatusNotFound, "knowledge node not found")
		return db.KnowledgeNode{}, pgtype.UUID{}, false
	}
	return node, wsUUID, true
}

func normalizeKnowledgeAliases(aliases []string) ([]byte, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" || seen[strings.ToLower(a)] {
			continue
		}
		seen[strings.ToLower(a)] = true
		out = append(out, a)
	}
	return json.Marshal(out)
}

func normalizeKnowledgeAttrs(attrs json.RawMessage) ([]byte, error) {
	if len(attrs) == 0 {
		return []byte("{}"), nil
	}
	var m map[string]any
	if err := json.Unmarshal(attrs, &m); err != nil {
		return nil, errors.New("attrs must be a JSON object")
	}
	return json.Marshal(m)
}

// ── Node handlers ──

// CreateKnowledgeNodeRequest is the body for POST /api/knowledge/nodes.
type CreateKnowledgeNodeRequest struct {
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Slug       string          `json:"slug"`
	Aliases    []string        `json:"aliases"`
	Summary    *string         `json:"summary"`
	Content    *string         `json:"content"`
	Attrs      json.RawMessage `json:"attrs"`
	Status     string          `json:"status"`
	ConfirmNew bool            `json:"confirm_new"`
}

// CreateKnowledgeNode creates a node with a dedup-first contract: unless
// confirm_new is set, a title/slug/alias match returns 409 with the
// candidate rows so callers (and agents) link instead of duplicating.
func (h *Handler) CreateKnowledgeNode(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}

	var req CreateKnowledgeNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if !knowledgeNodeKinds[req.Kind] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown kind %q", req.Kind))
		return
	}
	status, err := gateKnowledgeStatus(actorType, req.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !req.ConfirmNew {
		candidates, err := h.Queries.SearchKnowledgeNodeCandidates(r.Context(), db.SearchKnowledgeNodeCandidatesParams{
			WorkspaceID: wsUUID, Query: req.Title, Limit: 5,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check for duplicates")
			return
		}
		if len(candidates) > 0 {
			resp := make([]KnowledgeNodeResponse, len(candidates))
			for i, c := range candidates {
				resp[i] = knowledgeNodeToResponse(c)
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":      "possible duplicate nodes exist; link to one of the candidates or retry with confirm_new=true",
				"candidates": resp,
			})
			return
		}
	}

	slug := strings.TrimSpace(strings.ToLower(req.Slug))
	if slug == "" {
		slug, err = h.uniqueKnowledgeSlug(r.Context(), wsUUID, knowledgeSlugify(req.Title))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to allocate slug")
			return
		}
	} else if slug != knowledgeSlugify(slug) {
		writeError(w, http.StatusBadRequest, "slug must be kebab-case (lowercase alphanumerics and hyphens)")
		return
	}

	aliasesJSON, err := normalizeKnowledgeAliases(req.Aliases)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid aliases")
		return
	}
	attrsJSON, err := normalizeKnowledgeAttrs(req.Attrs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var summary, content pgtype.Text
	if req.Summary != nil && strings.TrimSpace(*req.Summary) != "" {
		summary = pgtype.Text{String: strings.TrimSpace(*req.Summary), Valid: true}
	}
	if req.Content != nil && strings.TrimSpace(*req.Content) != "" {
		content = pgtype.Text{String: *req.Content, Valid: true}
	}

	node, err := h.Queries.CreateKnowledgeNode(r.Context(), db.CreateKnowledgeNodeParams{
		WorkspaceID:   wsUUID,
		Kind:          req.Kind,
		Slug:          slug,
		Title:         req.Title,
		Aliases:       aliasesJSON,
		Summary:       summary,
		Content:       content,
		Attrs:         attrsJSON,
		Status:        status,
		CreatedByType: actorType,
		CreatedByID:   actorUUID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a node with this slug already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create knowledge node")
		return
	}

	if content.Valid {
		_, _ = h.Queries.CreateKnowledgeNodeRevision(r.Context(), db.CreateKnowledgeNodeRevisionParams{
			WorkspaceID: wsUUID, NodeID: node.ID,
			Content: content, Summary: summary,
			EditedByType: actorType, EditedByID: actorUUID,
		})
	}

	h.publish(protocol.EventKnowledgeNodeCreated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"node": knowledgeNodeToResponse(node)})
	writeJSON(w, http.StatusCreated, map[string]any{"node": knowledgeNodeToResponse(node)})
}

// GetKnowledgeNode returns a node by UUID or slug (merge redirects followed).
func (h *Handler) GetKnowledgeNode(w http.ResponseWriter, r *http.Request) {
	node, _, ok := h.loadKnowledgeNodeForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node": knowledgeNodeToResponse(node)})
}

// ListKnowledgeNodesHandler lists nodes with optional kind/status filters.
func (h *Handler) ListKnowledgeNodesHandler(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	limit := clampKnowledgeInt(r.URL.Query().Get("limit"), 100, 1, 500)
	offset := clampKnowledgeInt(r.URL.Query().Get("offset"), 0, 0, 1_000_000)

	var kind, status pgtype.Text
	if v := strings.TrimSpace(r.URL.Query().Get("kind")); v != "" {
		if !knowledgeNodeKinds[v] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown kind %q", v))
			return
		}
		kind = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		if !knowledgeStatuses[v] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown status %q", v))
			return
		}
		status = pgtype.Text{String: v, Valid: true}
	}

	nodes, err := h.Queries.ListKnowledgeNodes(r.Context(), db.ListKnowledgeNodesParams{
		WorkspaceID: wsUUID, Kind: kind, Status: status,
		Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge nodes")
		return
	}
	resp := make([]KnowledgeNodeResponse, len(nodes))
	for i, n := range nodes {
		resp[i] = knowledgeNodeToResponse(n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": resp, "total": len(resp)})
}

// SearchKnowledgeNodes is hybrid search: the lexical arm (slug / title /
// alias) always runs; when an embedding provider and pgvector are
// available, a semantic arm runs too and the rankings are fused with
// reciprocal-rank fusion. Semantic failures (provider down, no
// embeddings yet) silently degrade to lexical-only — search must never
// break because RAG is misconfigured.
func (h *Handler) SearchKnowledgeNodes(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := clampKnowledgeInt(r.URL.Query().Get("limit"), 20, 1, 100)
	lexical, err := h.Queries.SearchKnowledgeNodeCandidates(r.Context(), db.SearchKnowledgeNodeCandidatesParams{
		WorkspaceID: wsUUID, Query: query, Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search knowledge nodes")
		return
	}

	byID := make(map[string]db.KnowledgeNode, len(lexical))
	lexicalIDs := make([]string, len(lexical))
	for i, n := range lexical {
		id := uuidToString(n.ID)
		lexicalIDs[i] = id
		byID[id] = n
	}

	semanticUsed := false
	fusedIDs := lexicalIDs
	if h.Knowledge != nil && h.Knowledge.Enabled() {
		semanticIDs, serr := h.Knowledge.SemanticSearch(r.Context(), uuidToString(wsUUID), query, limit)
		if serr == nil && len(semanticIDs) > 0 {
			// Hydrate semantic-only hits, dropping merged/rejected nodes the
			// embedding table may still reference between backfill ticks.
			var missing []pgtype.UUID
			for _, id := range semanticIDs {
				if _, seen := byID[id]; !seen {
					if u, perr := util.ParseUUID(id); perr == nil {
						missing = append(missing, u)
					}
				}
			}
			if len(missing) > 0 {
				rows, rerr := h.Queries.ListKnowledgeNodesByIDs(r.Context(), db.ListKnowledgeNodesByIDsParams{
					WorkspaceID: wsUUID, Ids: missing,
				})
				if rerr == nil {
					for _, n := range rows {
						if !n.MergedInto.Valid && n.Status != "rejected" {
							byID[uuidToString(n.ID)] = n
						}
					}
				}
			}
			semanticUsed = true
			fusedIDs = knowledge.RRFMerge(lexicalIDs, semanticIDs)
		}
	}

	resp := make([]KnowledgeNodeResponse, 0, limit)
	for _, id := range fusedIDs {
		n, okNode := byID[id]
		if !okNode {
			continue
		}
		resp = append(resp, knowledgeNodeToResponse(n))
		if len(resp) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": resp, "total": len(resp), "semantic": semanticUsed})
}

// UpdateKnowledgeNodeRequest is the body for PUT /api/knowledge/nodes/{id}.
// Omitted fields keep their current value.
type UpdateKnowledgeNodeRequest struct {
	Title   *string         `json:"title"`
	Summary *string         `json:"summary"`
	Content *string         `json:"content"`
	Aliases []string        `json:"aliases"`
	Attrs   json.RawMessage `json:"attrs"`
	Status  *string         `json:"status"`
}

// UpdateKnowledgeNode edits a node. Status transitions (the review
// accept/reject path) are member-only; agents update content but never
// promote their own material.
func (h *Handler) UpdateKnowledgeNode(w http.ResponseWriter, r *http.Request) {
	node, wsUUID, ok := h.loadKnowledgeNodeForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	var req UpdateKnowledgeNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	title := node.Title
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = strings.TrimSpace(*req.Title)
	}
	summary := node.Summary
	if req.Summary != nil {
		summary = pgtype.Text{}
		if strings.TrimSpace(*req.Summary) != "" {
			summary = pgtype.Text{String: strings.TrimSpace(*req.Summary), Valid: true}
		}
	}
	content := node.Content
	contentChanged := false
	if req.Content != nil {
		newContent := pgtype.Text{}
		if strings.TrimSpace(*req.Content) != "" {
			newContent = pgtype.Text{String: *req.Content, Valid: true}
		}
		if newContent.String != content.String || newContent.Valid != content.Valid {
			contentChanged = true
		}
		content = newContent
	}
	aliases := node.Aliases
	if req.Aliases != nil {
		normalized, err := normalizeKnowledgeAliases(req.Aliases)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid aliases")
			return
		}
		aliases = normalized
	}
	attrs := node.Attrs
	if len(req.Attrs) > 0 {
		normalized, err := normalizeKnowledgeAttrs(req.Attrs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		attrs = normalized
	}
	status := node.Status
	if req.Status != nil {
		if actorType == "agent" {
			writeError(w, http.StatusForbidden, "agents cannot change knowledge status; a member reviews proposed material")
			return
		}
		if !knowledgeStatuses[*req.Status] {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown status %q", *req.Status))
			return
		}
		status = *req.Status
	}

	updated, err := h.Queries.UpdateKnowledgeNode(r.Context(), db.UpdateKnowledgeNodeParams{
		ID: node.ID, WorkspaceID: wsUUID,
		Title: title, Summary: summary, Content: content,
		Aliases: aliases, Attrs: attrs, Status: status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update knowledge node")
		return
	}
	if contentChanged {
		_, _ = h.Queries.CreateKnowledgeNodeRevision(r.Context(), db.CreateKnowledgeNodeRevisionParams{
			WorkspaceID: wsUUID, NodeID: node.ID,
			Content: content, Summary: summary,
			EditedByType: actorType, EditedByID: actorUUID,
		})
	}
	h.publish(protocol.EventKnowledgeNodeUpdated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"node": knowledgeNodeToResponse(updated)})
	writeJSON(w, http.StatusOK, map[string]any{"node": knowledgeNodeToResponse(updated)})
}

// DeleteKnowledgeNode removes a node and its edges. Member-only: deletion
// is destructive curation; agents close or supersede facts instead.
func (h *Handler) DeleteKnowledgeNode(w http.ResponseWriter, r *http.Request) {
	node, wsUUID, ok := h.loadKnowledgeNodeForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot delete knowledge nodes")
		return
	}
	if err := h.Queries.DeleteKnowledgeEdgesForEndpoint(r.Context(), db.DeleteKnowledgeEdgesForEndpointParams{
		WorkspaceID: wsUUID, SrcType: "node", SrcID: node.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete node edges")
		return
	}
	if err := h.Queries.DeleteKnowledgeNode(r.Context(), db.DeleteKnowledgeNodeParams{
		ID: node.ID, WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete knowledge node")
		return
	}
	h.publish(protocol.EventKnowledgeNodeDeleted, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"node_id": uuidToString(node.ID)})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// MergeKnowledgeNodeRequest is the body for POST /api/knowledge/nodes/{id}/merge.
type MergeKnowledgeNodeRequest struct {
	Into string `json:"into"`
}

// MergeKnowledgeNode folds the path node into the `into` node: edges are
// re-pointed (duplicates closed as superseded), the loser's title/aliases
// become aliases of the winner, and the loser stays behind as a redirect
// row so old references resolve. Member-only.
func (h *Handler) MergeKnowledgeNode(w http.ResponseWriter, r *http.Request) {
	loser, wsUUID, ok := h.loadKnowledgeNodeForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot merge knowledge nodes; propose via the review queue")
		return
	}
	var req MergeKnowledgeNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Into) == "" {
		writeError(w, http.StatusBadRequest, "into is required")
		return
	}
	winner, err := h.loadKnowledgeNodeByRef(r.Context(), wsUUID, req.Into)
	if err != nil {
		writeError(w, http.StatusNotFound, "merge target not found")
		return
	}
	if winner.ID == loser.ID {
		writeError(w, http.StatusBadRequest, "cannot merge a node into itself")
		return
	}

	edges, err := h.Queries.ListKnowledgeEdgesTouching(r.Context(), db.ListKnowledgeEdgesTouchingParams{
		WorkspaceID: wsUUID, SrcType: "node", SrcID: loser.ID,
		Predicate: pgtype.Text{}, IncludeClosed: true, Limit: 2000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list node edges")
		return
	}
	for _, e := range edges {
		newSrcType, newSrcID := e.SrcType, e.SrcID
		newDstType, newDstID := e.DstType, e.DstID
		if e.SrcType == "node" && e.SrcID == loser.ID {
			newSrcID = winner.ID
		}
		if e.DstType == "node" && e.DstID == loser.ID {
			newDstID = winner.ID
		}
		// Self-loops produced by merging two directly-linked nodes carry
		// no information; close them instead of re-pointing.
		if newSrcType == newDstType && newSrcID == newDstID {
			_, _ = h.Queries.CloseKnowledgeEdge(r.Context(), db.CloseKnowledgeEdgeParams{
				ID: e.ID, WorkspaceID: wsUUID, SupersededBy: pgtype.UUID{},
			})
			continue
		}
		// If the winner already holds the identical live fact, the moved
		// edge is a duplicate: close it as superseded by the survivor.
		if !e.ValidUntil.Valid {
			existing, gerr := h.Queries.GetLiveKnowledgeEdgeByEndpoints(r.Context(), db.GetLiveKnowledgeEdgeByEndpointsParams{
				WorkspaceID: wsUUID,
				SrcType:     newSrcType, SrcID: newSrcID,
				DstType: newDstType, DstID: newDstID,
				Predicate: e.Predicate,
			})
			if gerr == nil && existing.ID != e.ID {
				_, _ = h.Queries.CloseKnowledgeEdge(r.Context(), db.CloseKnowledgeEdgeParams{
					ID: e.ID, WorkspaceID: wsUUID,
					SupersededBy: existing.ID,
				})
				continue
			}
		}
		_, _ = h.Queries.UpdateKnowledgeEdgeEndpoints(r.Context(), db.UpdateKnowledgeEdgeEndpointsParams{
			ID: e.ID, WorkspaceID: wsUUID,
			SrcType: newSrcType, SrcID: newSrcID,
			DstType: newDstType, DstID: newDstID,
		})
	}

	// Fold the loser's identity into the winner's aliases.
	var winnerAliases, loserAliases []string
	_ = json.Unmarshal(winner.Aliases, &winnerAliases)
	_ = json.Unmarshal(loser.Aliases, &loserAliases)
	merged := append(winnerAliases, loser.Title)
	merged = append(merged, loserAliases...)
	aliasesJSON, err := normalizeKnowledgeAliases(merged)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to merge aliases")
		return
	}
	updatedWinner, err := h.Queries.UpdateKnowledgeNode(r.Context(), db.UpdateKnowledgeNodeParams{
		ID: winner.ID, WorkspaceID: wsUUID,
		Title: winner.Title, Summary: winner.Summary, Content: winner.Content,
		Aliases: aliasesJSON, Attrs: winner.Attrs, Status: winner.Status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update merge target")
		return
	}
	if _, err := h.Queries.MarkKnowledgeNodeMerged(r.Context(), db.MarkKnowledgeNodeMergedParams{
		ID: loser.ID, WorkspaceID: wsUUID, MergedInto: winner.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark node merged")
		return
	}

	h.publish(protocol.EventKnowledgeNodeUpdated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"node": knowledgeNodeToResponse(updatedWinner)})
	h.publish(protocol.EventKnowledgeNodeDeleted, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"node_id": uuidToString(loser.ID), "merged_into": uuidToString(winner.ID)})
	writeJSON(w, http.StatusOK, map[string]any{"node": knowledgeNodeToResponse(updatedWinner)})
}

// ListKnowledgeNodeRevisionsHandler returns a node's wiki-page history.
func (h *Handler) ListKnowledgeNodeRevisionsHandler(w http.ResponseWriter, r *http.Request) {
	node, wsUUID, ok := h.loadKnowledgeNodeForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	revisions, err := h.Queries.ListKnowledgeNodeRevisions(r.Context(), db.ListKnowledgeNodeRevisionsParams{
		NodeID: node.ID, WorkspaceID: wsUUID, Limit: 50,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list revisions")
		return
	}
	type revisionResponse struct {
		ID           string  `json:"id"`
		Content      *string `json:"content"`
		Summary      *string `json:"summary"`
		EditedByType string  `json:"edited_by_type"`
		EditedByID   string  `json:"edited_by_id"`
		CreatedAt    string  `json:"created_at"`
	}
	resp := make([]revisionResponse, len(revisions))
	for i, rev := range revisions {
		resp[i] = revisionResponse{
			ID:           uuidToString(rev.ID),
			Content:      textToPtr(rev.Content),
			Summary:      textToPtr(rev.Summary),
			EditedByType: rev.EditedByType,
			EditedByID:   uuidToString(rev.EditedByID),
			CreatedAt:    timestampToString(rev.CreatedAt),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": resp, "total": len(resp)})
}

// ── Source handlers ──

// CreateKnowledgeSourceRequest is the body for POST /api/knowledge/sources.
type CreateKnowledgeSourceRequest struct {
	SourceType string          `json:"source_type"`
	SourceRef  json.RawMessage `json:"source_ref"`
	Title      *string         `json:"title"`
	Content    *string         `json:"content"`
}

// CreateKnowledgeSource captures a provenance anchor. URL sources are
// deduplicated: capturing the same URL twice returns the existing row.
func (h *Handler) CreateKnowledgeSource(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	var req CreateKnowledgeSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.SourceType = strings.TrimSpace(req.SourceType)
	if !knowledgeSourceTypes[req.SourceType] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown source_type %q", req.SourceType))
		return
	}
	ref := req.SourceRef
	if len(ref) == 0 {
		ref = json.RawMessage("{}")
	}
	var refMap map[string]any
	if err := json.Unmarshal(ref, &refMap); err != nil {
		writeError(w, http.StatusBadRequest, "source_ref must be a JSON object")
		return
	}
	if req.SourceType == "url" {
		u, _ := refMap["url"].(string)
		u = strings.TrimSpace(u)
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			writeError(w, http.StatusBadRequest, "url source requires source_ref.url with http(s) scheme")
			return
		}
		if existing, err := h.Queries.GetKnowledgeSourceByURL(r.Context(), db.GetKnowledgeSourceByURLParams{
			WorkspaceID: wsUUID, Url: u,
		}); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"source": knowledgeSourceToResponse(existing), "existing": true})
			return
		}
	}

	var title, content pgtype.Text
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = pgtype.Text{String: strings.TrimSpace(*req.Title), Valid: true}
	}
	if req.Content != nil && strings.TrimSpace(*req.Content) != "" {
		content = pgtype.Text{String: *req.Content, Valid: true}
	}
	source, err := h.Queries.CreateKnowledgeSource(r.Context(), db.CreateKnowledgeSourceParams{
		WorkspaceID: wsUUID, SourceType: req.SourceType, SourceRef: ref,
		Title: title, Content: content,
		CreatedByType: actorType, CreatedByID: actorUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create knowledge source")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"source": knowledgeSourceToResponse(source)})
}

// GetKnowledgeSource returns a single provenance source.
func (h *Handler) GetKnowledgeSource(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	sourceUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "source id")
	if !ok {
		return
	}
	source, err := h.Queries.GetKnowledgeSourceInWorkspace(r.Context(), db.GetKnowledgeSourceInWorkspaceParams{
		ID: sourceUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "knowledge source not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": knowledgeSourceToResponse(source)})
}

func clampKnowledgeInt(raw string, def, min, max int) int {
	if strings.TrimSpace(raw) == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
