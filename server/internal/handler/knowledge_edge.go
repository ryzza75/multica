package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type KnowledgeEdgeResponse struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	SrcType        string          `json:"src_type"`
	SrcID          string          `json:"src_id"`
	DstType        string          `json:"dst_type"`
	DstID          string          `json:"dst_id"`
	Predicate      string          `json:"predicate"`
	Confidence     float32         `json:"confidence"`
	Attrs          json.RawMessage `json:"attrs"`
	Status         string          `json:"status"`
	ValidFrom      *string         `json:"valid_from"`
	ValidUntil     *string         `json:"valid_until"`
	SupersededBy   *string         `json:"superseded_by"`
	LastAffirmedAt string          `json:"last_affirmed_at"`
	CreatedByType  string          `json:"created_by_type"`
	CreatedByID    string          `json:"created_by_id"`
	CreatedAt      string          `json:"created_at"`
}

func knowledgeEdgeToResponse(e db.KnowledgeEdge) KnowledgeEdgeResponse {
	attrs := json.RawMessage(e.Attrs)
	if len(attrs) == 0 {
		attrs = json.RawMessage("{}")
	}
	return KnowledgeEdgeResponse{
		ID:             uuidToString(e.ID),
		WorkspaceID:    uuidToString(e.WorkspaceID),
		SrcType:        e.SrcType,
		SrcID:          uuidToString(e.SrcID),
		DstType:        e.DstType,
		DstID:          uuidToString(e.DstID),
		Predicate:      e.Predicate,
		Confidence:     e.Confidence,
		Attrs:          attrs,
		Status:         e.Status,
		ValidFrom:      timestampToPtr(e.ValidFrom),
		ValidUntil:     timestampToPtr(e.ValidUntil),
		SupersededBy:   uuidToPtr(e.SupersededBy),
		LastAffirmedAt: timestampToString(e.LastAffirmedAt),
		CreatedByType:  e.CreatedByType,
		CreatedByID:    uuidToString(e.CreatedByID),
		CreatedAt:      timestampToString(e.CreatedAt),
	}
}

// resolveKnowledgeEndpoint validates and resolves one edge endpoint. Node
// endpoints accept a slug or UUID and must exist (merge redirects
// followed); other entity types accept a UUID only — their existence is
// not FK-enforceable on a polymorphic column, and dangling refs are
// garbage-collected by the read path per the plan.
func (h *Handler) resolveKnowledgeEndpoint(ctx context.Context, wsUUID pgtype.UUID, epType, epRef string) (string, pgtype.UUID, error) {
	epType = strings.TrimSpace(epType)
	if epType == "" {
		epType = "node"
	}
	if !knowledgeEndpointTypes[epType] {
		return "", pgtype.UUID{}, fmt.Errorf("unknown endpoint type %q", epType)
	}
	epRef = strings.TrimSpace(epRef)
	if epRef == "" {
		return "", pgtype.UUID{}, errors.New("endpoint id is required")
	}
	if epType == "node" {
		node, err := h.loadKnowledgeNodeByRef(ctx, wsUUID, epRef)
		if err != nil {
			return "", pgtype.UUID{}, fmt.Errorf("node %q not found", epRef)
		}
		return "node", node.ID, nil
	}
	id, err := util.ParseUUID(epRef)
	if err != nil {
		return "", pgtype.UUID{}, fmt.Errorf("%s endpoint must be a UUID", epType)
	}
	return epType, id, nil
}

// recomputeKnowledgeConfidence derives an edge's cached confidence from
// its creator and evidence tally. Heuristic v1 (the RAG phase adds recency
// weighting): member assertions start high, agent claims lower; each
// supporting source beyond the first adds a little, each contradiction
// subtracts a lot.
func recomputeKnowledgeConfidence(createdByType string, supports, contradicts int) float32 {
	base := 0.6
	if createdByType == "member" {
		base = 0.9
	}
	extra := supports - 1
	if extra < 0 {
		extra = 0
	}
	c := base + 0.05*float64(extra) - 0.25*float64(contradicts)
	if c > 0.98 {
		c = 0.98
	}
	if c < 0.05 {
		c = 0.05
	}
	return float32(c)
}

// resolveOrCreateEdgeSource turns the request's provenance shorthand into a
// knowledge_source row: an explicit source_id wins, else source_url is
// deduped-or-created. Returns a zero UUID when no provenance was supplied.
func (h *Handler) resolveOrCreateEdgeSource(ctx context.Context, wsUUID pgtype.UUID, actorType string, actorUUID pgtype.UUID, sourceID, sourceURL, sourceTitle string) (pgtype.UUID, error) {
	if strings.TrimSpace(sourceID) != "" {
		id, err := util.ParseUUID(sourceID)
		if err != nil {
			return pgtype.UUID{}, errors.New("source_id must be a UUID")
		}
		if _, err := h.Queries.GetKnowledgeSourceInWorkspace(ctx, db.GetKnowledgeSourceInWorkspaceParams{
			ID: id, WorkspaceID: wsUUID,
		}); err != nil {
			return pgtype.UUID{}, errors.New("source not found")
		}
		return id, nil
	}
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return pgtype.UUID{}, nil
	}
	if !strings.HasPrefix(sourceURL, "http://") && !strings.HasPrefix(sourceURL, "https://") {
		return pgtype.UUID{}, errors.New("source_url must use http(s)")
	}
	if existing, err := h.Queries.GetKnowledgeSourceByURL(ctx, db.GetKnowledgeSourceByURLParams{
		WorkspaceID: wsUUID, Url: sourceURL,
	}); err == nil {
		return existing.ID, nil
	}
	refJSON, err := json.Marshal(map[string]string{"url": sourceURL})
	if err != nil {
		return pgtype.UUID{}, err
	}
	var title pgtype.Text
	if strings.TrimSpace(sourceTitle) != "" {
		title = pgtype.Text{String: strings.TrimSpace(sourceTitle), Valid: true}
	}
	source, err := h.Queries.CreateKnowledgeSource(ctx, db.CreateKnowledgeSourceParams{
		WorkspaceID: wsUUID, SourceType: "url", SourceRef: refJSON,
		Title: title, CreatedByType: actorType, CreatedByID: actorUUID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return source.ID, nil
}

// attachEvidenceAndRecompute files one evidence row and refreshes the
// edge's cached confidence + last_affirmed_at from the full tally.
func (h *Handler) attachEvidenceAndRecompute(ctx context.Context, wsUUID pgtype.UUID, edge db.KnowledgeEdge, sourceID pgtype.UUID, stance, note, actorType string, actorUUID pgtype.UUID) (db.KnowledgeEdge, error) {
	var notePg pgtype.Text
	if strings.TrimSpace(note) != "" {
		notePg = pgtype.Text{String: strings.TrimSpace(note), Valid: true}
	}
	if _, err := h.Queries.CreateKnowledgeEvidence(ctx, db.CreateKnowledgeEvidenceParams{
		WorkspaceID: wsUUID, EdgeID: edge.ID, SourceID: sourceID,
		Stance: stance, Note: notePg,
		CreatedByType: actorType, CreatedByID: actorUUID,
	}); err != nil {
		return db.KnowledgeEdge{}, err
	}
	stances, err := h.Queries.ListKnowledgeEvidenceStances(ctx, db.ListKnowledgeEvidenceStancesParams{
		EdgeID: edge.ID, WorkspaceID: wsUUID,
	})
	if err != nil {
		return db.KnowledgeEdge{}, err
	}
	supports, contradicts := 0, 0
	for _, s := range stances {
		if s.Stance == "contradicts" {
			contradicts++
		} else {
			supports++
		}
	}
	return h.Queries.AffirmKnowledgeEdge(ctx, db.AffirmKnowledgeEdgeParams{
		ID: edge.ID, WorkspaceID: wsUUID,
		Confidence: recomputeKnowledgeConfidence(edge.CreatedByType, supports, contradicts),
	})
}

// ── Edge handlers ──

// CreateKnowledgeEdgeRequest is the body for POST /api/knowledge/edges.
// src_id / dst_id accept a node slug or UUID when the type is node.
type CreateKnowledgeEdgeRequest struct {
	SrcType     string          `json:"src_type"`
	SrcID       string          `json:"src_id"`
	DstType     string          `json:"dst_type"`
	DstID       string          `json:"dst_id"`
	Predicate   string          `json:"predicate"`
	Confidence  *float32        `json:"confidence"`
	Attrs       json.RawMessage `json:"attrs"`
	Status      string          `json:"status"`
	SourceID    string          `json:"source_id"`
	SourceURL   string          `json:"source_url"`
	SourceTitle string          `json:"source_title"`
	Note        string          `json:"note"`
	Supersede   bool            `json:"supersede"`
}

// CreateKnowledgeEdge is the reconciled write path (intake pipeline stage 3):
//   - an identical live edge is AFFIRMED (evidence attached, confidence
//     recomputed) instead of duplicated — response carries affirmed=true;
//   - a conflicting live edge on a functional predicate is 409 for members
//     unless supersede=true (which closes the old fact), and lands as a
//     `proposed` competitor for agents so a human resolves it in review;
//   - anything genuinely new inserts a live edge.
func (h *Handler) CreateKnowledgeEdge(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	var req CreateKnowledgeEdgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	spec, known := knowledgePredicates[strings.TrimSpace(req.Predicate)]
	if !known {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown predicate %q", req.Predicate))
		return
	}
	predicate := strings.TrimSpace(req.Predicate)

	srcType, srcID, err := h.resolveKnowledgeEndpoint(r.Context(), wsUUID, req.SrcType, req.SrcID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "src: "+err.Error())
		return
	}
	dstType, dstID, err := h.resolveKnowledgeEndpoint(r.Context(), wsUUID, req.DstType, req.DstID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "dst: "+err.Error())
		return
	}
	if srcType == dstType && srcID == dstID {
		writeError(w, http.StatusBadRequest, "src and dst must differ")
		return
	}
	attrsJSON, err := normalizeKnowledgeAttrs(req.Attrs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := gateKnowledgeStatus(actorType, req.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sourceID, err := h.resolveOrCreateEdgeSource(r.Context(), wsUUID, actorType, actorUUID, req.SourceID, req.SourceURL, req.SourceTitle)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Reconcile: identical live fact → affirm, don't duplicate.
	existing, err := h.Queries.GetLiveKnowledgeEdgeByEndpoints(r.Context(), db.GetLiveKnowledgeEdgeByEndpointsParams{
		WorkspaceID: wsUUID,
		SrcType:     srcType, SrcID: srcID,
		DstType: dstType, DstID: dstID,
		Predicate: predicate,
	})
	if err == nil {
		affirmed := existing
		if sourceID.Valid {
			affirmed, err = h.attachEvidenceAndRecompute(r.Context(), wsUUID, existing, sourceID, "supports", req.Note, actorType, actorUUID)
		} else {
			affirmed, err = h.Queries.AffirmKnowledgeEdge(r.Context(), db.AffirmKnowledgeEdgeParams{
				ID: existing.ID, WorkspaceID: wsUUID, Confidence: existing.Confidence,
			})
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to affirm existing edge")
			return
		}
		h.publish(protocol.EventKnowledgeEdgeUpdated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
			map[string]any{"edge": knowledgeEdgeToResponse(affirmed)})
		writeJSON(w, http.StatusOK, map[string]any{"edge": knowledgeEdgeToResponse(affirmed), "affirmed": true})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check existing edges")
		return
	}

	// Functional-predicate conflict: at most one live edge per (src,
	// predicate).
	var conflicts []db.KnowledgeEdge
	if spec.Functional {
		conflicts, err = h.Queries.ListLiveKnowledgeEdgesFromSource(r.Context(), db.ListLiveKnowledgeEdgesFromSourceParams{
			WorkspaceID: wsUUID, SrcType: srcType, SrcID: srcID, Predicate: predicate,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check functional conflicts")
			return
		}
		if len(conflicts) > 0 && actorType != "agent" && !req.Supersede {
			resp := make([]KnowledgeEdgeResponse, len(conflicts))
			for i, c := range conflicts {
				resp[i] = knowledgeEdgeToResponse(c)
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     fmt.Sprintf("%s is single-valued and a live edge already exists; retry with supersede=true to close it", predicate),
				"conflicts": resp,
			})
			return
		}
	}

	confidence := recomputeKnowledgeConfidence(actorType, 1, 0)
	if req.Confidence != nil {
		if *req.Confidence <= 0 || *req.Confidence > 1 {
			writeError(w, http.StatusBadRequest, "confidence must be in (0, 1]")
			return
		}
		confidence = *req.Confidence
	}
	edge, err := h.Queries.CreateKnowledgeEdge(r.Context(), db.CreateKnowledgeEdgeParams{
		WorkspaceID: wsUUID,
		SrcType:     srcType, SrcID: srcID,
		DstType: dstType, DstID: dstID,
		Predicate: predicate, Confidence: confidence,
		Attrs: attrsJSON, Status: status,
		ValidFrom:     pgtype.Timestamptz{},
		CreatedByType: actorType, CreatedByID: actorUUID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an identical live edge already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create knowledge edge")
		return
	}
	if sourceID.Valid {
		if updated, err := h.attachEvidenceAndRecompute(r.Context(), wsUUID, edge, sourceID, "supports", req.Note, actorType, actorUUID); err == nil {
			edge = updated
		}
	}

	// Member supersede: close each conflicting old fact, pointing at the
	// replacement. Agent conflicts stay open — the proposed competitor and
	// the live fact meet in the review queue.
	supersededIDs := make([]string, 0, len(conflicts))
	if spec.Functional && len(conflicts) > 0 && actorType != "agent" && req.Supersede {
		for _, c := range conflicts {
			closed, cerr := h.Queries.CloseKnowledgeEdge(r.Context(), db.CloseKnowledgeEdgeParams{
				ID: c.ID, WorkspaceID: wsUUID, SupersededBy: edge.ID,
			})
			if cerr == nil {
				supersededIDs = append(supersededIDs, uuidToString(closed.ID))
			}
		}
	}

	h.publish(protocol.EventKnowledgeEdgeCreated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"edge": knowledgeEdgeToResponse(edge)})
	resp := map[string]any{"edge": knowledgeEdgeToResponse(edge)}
	if len(supersededIDs) > 0 {
		resp["superseded"] = supersededIDs
	}
	if spec.Functional && len(conflicts) > 0 && actorType == "agent" {
		conflictResp := make([]KnowledgeEdgeResponse, len(conflicts))
		for i, c := range conflicts {
			conflictResp[i] = knowledgeEdgeToResponse(c)
		}
		resp["conflicts_pending_review"] = conflictResp
	}
	writeJSON(w, http.StatusCreated, resp)
}

// loadKnowledgeEdgeForUser resolves an edge id path param within the
// request's workspace.
func (h *Handler) loadKnowledgeEdgeForUser(w http.ResponseWriter, r *http.Request) (db.KnowledgeEdge, pgtype.UUID, bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return db.KnowledgeEdge{}, pgtype.UUID{}, false
	}
	edgeUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "edge id")
	if !ok {
		return db.KnowledgeEdge{}, pgtype.UUID{}, false
	}
	edge, err := h.Queries.GetKnowledgeEdgeInWorkspace(r.Context(), db.GetKnowledgeEdgeInWorkspaceParams{
		ID: edgeUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "knowledge edge not found")
		return db.KnowledgeEdge{}, pgtype.UUID{}, false
	}
	return edge, wsUUID, true
}

// GetKnowledgeEdge returns an edge with its evidence.
func (h *Handler) GetKnowledgeEdge(w http.ResponseWriter, r *http.Request) {
	edge, wsUUID, ok := h.loadKnowledgeEdgeForUser(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListKnowledgeEvidenceForEdge(r.Context(), db.ListKnowledgeEvidenceForEdgeParams{
		EdgeID: edge.ID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list evidence")
		return
	}
	type evidenceResponse struct {
		ID        string                  `json:"id"`
		Stance    string                  `json:"stance"`
		Note      *string                 `json:"note"`
		CreatedAt string                  `json:"created_at"`
		Source    KnowledgeSourceResponse `json:"source"`
	}
	evidence := make([]evidenceResponse, len(rows))
	for i, row := range rows {
		evidence[i] = evidenceResponse{
			ID:        uuidToString(row.KnowledgeEvidence.ID),
			Stance:    row.KnowledgeEvidence.Stance,
			Note:      textToPtr(row.KnowledgeEvidence.Note),
			CreatedAt: timestampToString(row.KnowledgeEvidence.CreatedAt),
			Source:    knowledgeSourceToResponse(row.KnowledgeSource),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"edge":     knowledgeEdgeToResponse(edge),
		"evidence": evidence,
	})
}

// ListKnowledgeEdges lists edges touching one endpoint (required filter —
// unbounded workspace-wide edge dumps go through /graph instead).
func (h *Handler) ListKnowledgeEdges(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	q := r.URL.Query()
	epType, epID, err := h.resolveKnowledgeEndpoint(r.Context(), wsUUID, q.Get("endpoint_type"), q.Get("endpoint_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "endpoint: "+err.Error())
		return
	}
	var predicate pgtype.Text
	if v := strings.TrimSpace(q.Get("predicate")); v != "" {
		if _, known := knowledgePredicates[v]; !known {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown predicate %q", v))
			return
		}
		predicate = pgtype.Text{String: v, Valid: true}
	}
	limit := clampKnowledgeInt(q.Get("limit"), 100, 1, 500)
	edges, err := h.Queries.ListKnowledgeEdgesTouching(r.Context(), db.ListKnowledgeEdgesTouchingParams{
		WorkspaceID: wsUUID, SrcType: epType, SrcID: epID,
		Predicate:     predicate,
		IncludeClosed: q.Get("include_closed") == "true",
		Limit:         int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge edges")
		return
	}
	resp := make([]KnowledgeEdgeResponse, len(edges))
	for i, e := range edges {
		resp[i] = knowledgeEdgeToResponse(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": resp, "total": len(resp)})
}

// AddKnowledgeEvidenceRequest is the body for POST /api/knowledge/edges/{id}/evidence.
type AddKnowledgeEvidenceRequest struct {
	SourceID    string `json:"source_id"`
	SourceURL   string `json:"source_url"`
	SourceTitle string `json:"source_title"`
	Stance      string `json:"stance"`
	Note        string `json:"note"`
}

// AddKnowledgeEvidence attaches supporting or contradicting evidence and
// recomputes the edge's cached confidence.
func (h *Handler) AddKnowledgeEvidence(w http.ResponseWriter, r *http.Request) {
	edge, wsUUID, ok := h.loadKnowledgeEdgeForUser(w, r)
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	var req AddKnowledgeEvidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	stance := strings.TrimSpace(req.Stance)
	if stance == "" {
		stance = "supports"
	}
	if stance != "supports" && stance != "contradicts" {
		writeError(w, http.StatusBadRequest, "stance must be supports or contradicts")
		return
	}
	sourceID, err := h.resolveOrCreateEdgeSource(r.Context(), wsUUID, actorType, actorUUID, req.SourceID, req.SourceURL, req.SourceTitle)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !sourceID.Valid {
		writeError(w, http.StatusBadRequest, "source_id or source_url is required")
		return
	}
	updated, err := h.attachEvidenceAndRecompute(r.Context(), wsUUID, edge, sourceID, stance, req.Note, actorType, actorUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach evidence")
		return
	}
	h.publish(protocol.EventKnowledgeEdgeUpdated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"edge": knowledgeEdgeToResponse(updated)})
	writeJSON(w, http.StatusOK, map[string]any{"edge": knowledgeEdgeToResponse(updated)})
}

// CloseKnowledgeEdgeRequest is the body for POST /api/knowledge/edges/{id}/close.
type CloseKnowledgeEdgeRequest struct {
	SupersededBy string `json:"superseded_by"`
}

// CloseKnowledgeEdgeHandler ends a fact's validity window (member-only —
// agents supersede through the edge-create flow, never silently retire
// facts).
func (h *Handler) CloseKnowledgeEdgeHandler(w http.ResponseWriter, r *http.Request) {
	edge, wsUUID, ok := h.loadKnowledgeEdgeForUser(w, r)
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot close edges directly; create a superseding edge instead")
		return
	}
	if edge.ValidUntil.Valid {
		writeError(w, http.StatusConflict, "edge is already closed")
		return
	}
	var req CloseKnowledgeEdgeRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	var supersededBy pgtype.UUID
	if strings.TrimSpace(req.SupersededBy) != "" {
		id, err := util.ParseUUID(req.SupersededBy)
		if err != nil {
			writeError(w, http.StatusBadRequest, "superseded_by must be a UUID")
			return
		}
		if _, err := h.Queries.GetKnowledgeEdgeInWorkspace(r.Context(), db.GetKnowledgeEdgeInWorkspaceParams{
			ID: id, WorkspaceID: wsUUID,
		}); err != nil {
			writeError(w, http.StatusNotFound, "superseding edge not found")
			return
		}
		supersededBy = id
	}
	closed, err := h.Queries.CloseKnowledgeEdge(r.Context(), db.CloseKnowledgeEdgeParams{
		ID: edge.ID, WorkspaceID: wsUUID, SupersededBy: supersededBy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close knowledge edge")
		return
	}
	h.publish(protocol.EventKnowledgeEdgeUpdated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"edge": knowledgeEdgeToResponse(closed)})
	writeJSON(w, http.StatusOK, map[string]any{"edge": knowledgeEdgeToResponse(closed)})
}

// UpdateKnowledgeEdgeStatusRequest is the body for PUT /api/knowledge/edges/{id}/status.
type UpdateKnowledgeEdgeStatusRequest struct {
	Status string `json:"status"`
}

// UpdateKnowledgeEdgeStatusHandler is the review action: members promote
// proposed edges to confirmed or reject them.
func (h *Handler) UpdateKnowledgeEdgeStatusHandler(w http.ResponseWriter, r *http.Request) {
	edge, wsUUID, ok := h.loadKnowledgeEdgeForUser(w, r)
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot change edge status; a member reviews proposed material")
		return
	}
	var req UpdateKnowledgeEdgeStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !knowledgeStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "status must be proposed, confirmed, or rejected")
		return
	}
	updated, err := h.Queries.UpdateKnowledgeEdgeStatus(r.Context(), db.UpdateKnowledgeEdgeStatusParams{
		ID: edge.ID, WorkspaceID: wsUUID, Status: req.Status,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update edge status")
		return
	}
	h.publish(protocol.EventKnowledgeEdgeUpdated, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"edge": knowledgeEdgeToResponse(updated)})
	writeJSON(w, http.StatusOK, map[string]any{"edge": knowledgeEdgeToResponse(updated)})
}

// DeleteKnowledgeEdgeHandler hard-deletes an edge (member-only; normal
// retirement is close/supersede, delete is for mistakes).
func (h *Handler) DeleteKnowledgeEdgeHandler(w http.ResponseWriter, r *http.Request) {
	edge, wsUUID, ok := h.loadKnowledgeEdgeForUser(w, r)
	if !ok {
		return
	}
	actorType, actorUUID, ok := h.resolveKnowledgeActor(w, r, uuidToString(wsUUID))
	if !ok {
		return
	}
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents cannot delete knowledge edges")
		return
	}
	if err := h.Queries.DeleteKnowledgeEdge(r.Context(), db.DeleteKnowledgeEdgeParams{
		ID: edge.ID, WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete knowledge edge")
		return
	}
	h.publish(protocol.EventKnowledgeEdgeDeleted, uuidToString(wsUUID), actorType, uuidToString(actorUUID),
		map[string]any{"edge_id": uuidToString(edge.ID)})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
