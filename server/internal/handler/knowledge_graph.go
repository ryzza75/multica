package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Graph traversal caps (docs/knowledge-graph-plan.md Phase 1): responses
// are bounded and report truncation so callers know coverage was limited
// rather than assuming they saw everything.
const (
	knowledgeGraphMaxHops      = 4
	knowledgeGraphDefaultHops  = 2
	knowledgeGraphMaxEdges     = 500
	knowledgeGraphDefaultEdges = 200
)

// knowledgeEndpointKey identifies one polymorphic endpoint in traversal
// bookkeeping.
type knowledgeEndpointKey struct {
	Type string
	ID   string
}

// knowledgeGraphExpand runs bounded breadth-first expansion from a set of
// seed endpoints, one frontier query per endpoint type per hop. Returns the
// collected edges (keyed by id), every endpoint seen, per-endpoint parent
// edges for path reconstruction, and whether the edge cap truncated the
// walk.
func (h *Handler) knowledgeGraphExpand(
	ctx context.Context,
	wsUUID pgtype.UUID,
	seeds []knowledgeEndpointKey,
	hops int,
	statuses []string,
	asOf pgtype.Timestamptz,
	minConfidence float32,
	edgeLimit int,
) (map[string]db.KnowledgeEdge, map[knowledgeEndpointKey]bool, map[knowledgeEndpointKey]string, bool, error) {
	edges := make(map[string]db.KnowledgeEdge)
	visited := make(map[knowledgeEndpointKey]bool, len(seeds))
	parentEdge := make(map[knowledgeEndpointKey]string)
	frontier := make([]knowledgeEndpointKey, 0, len(seeds))
	for _, s := range seeds {
		if !visited[s] {
			visited[s] = true
			frontier = append(frontier, s)
		}
	}
	truncated := false

	for hop := 0; hop < hops && len(frontier) > 0 && !truncated; hop++ {
		byType := map[string][]pgtype.UUID{}
		for _, ep := range frontier {
			id, err := parseUUIDSafe(ep.ID)
			if err != nil {
				continue
			}
			byType[ep.Type] = append(byType[ep.Type], id)
		}
		var next []knowledgeEndpointKey
		for epType, ids := range byType {
			rows, err := h.Queries.ListKnowledgeEdgesForFrontier(ctx, db.ListKnowledgeEdgesForFrontierParams{
				WorkspaceID:   wsUUID,
				SrcType:       epType,
				Ids:           ids,
				Statuses:      statuses,
				AsOf:          asOf,
				MinConfidence: minConfidence,
				Limit:         int32(edgeLimit),
			})
			if err != nil {
				return nil, nil, nil, false, err
			}
			for _, e := range rows {
				id := uuidToString(e.ID)
				if _, seen := edges[id]; seen {
					continue
				}
				if len(edges) >= edgeLimit {
					truncated = true
					break
				}
				edges[id] = e
				for _, ep := range []knowledgeEndpointKey{
					{Type: e.SrcType, ID: uuidToString(e.SrcID)},
					{Type: e.DstType, ID: uuidToString(e.DstID)},
				} {
					if !visited[ep] {
						visited[ep] = true
						parentEdge[ep] = id
						next = append(next, ep)
					}
				}
			}
			if truncated {
				break
			}
		}
		frontier = next
	}
	return edges, visited, parentEdge, truncated, nil
}

func parseUUIDSafe(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	err := u.Scan(s)
	return u, err
}

// hydrateKnowledgeGraph splits visited endpoints into hydrated knowledge
// nodes and bare refs (internal entities the UI resolves through its own
// queries). Dangling node endpoints — edges whose node has been deleted —
// are dropped from the node list; their edges still render as refs.
func (h *Handler) hydrateKnowledgeGraph(ctx context.Context, wsUUID pgtype.UUID, visited map[knowledgeEndpointKey]bool) ([]KnowledgeNodeResponse, []map[string]string, error) {
	var nodeIDs []pgtype.UUID
	refs := make([]map[string]string, 0)
	for ep := range visited {
		if ep.Type == "node" {
			if id, err := parseUUIDSafe(ep.ID); err == nil {
				nodeIDs = append(nodeIDs, id)
			}
			continue
		}
		refs = append(refs, map[string]string{"type": ep.Type, "id": ep.ID})
	}
	nodes := make([]KnowledgeNodeResponse, 0, len(nodeIDs))
	if len(nodeIDs) > 0 {
		rows, err := h.Queries.ListKnowledgeNodesByIDs(ctx, db.ListKnowledgeNodesByIDsParams{
			WorkspaceID: wsUUID, Ids: nodeIDs,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, n := range rows {
			nodes = append(nodes, knowledgeNodeToResponse(n))
		}
	}
	return nodes, refs, nil
}

func knowledgeStatusesFromQuery(includeProposed bool) []string {
	if includeProposed {
		return []string{"confirmed", "proposed"}
	}
	return []string{"confirmed"}
}

// GetKnowledgeGraph returns the bounded N-hop neighborhood around a focus
// endpoint: GET /api/knowledge/graph?focus=&focus_type=&hops=&include_proposed=&min_confidence=&as_of=&limit=
func (h *Handler) GetKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	q := r.URL.Query()
	focusType, focusID, err := h.resolveKnowledgeEndpoint(r.Context(), wsUUID, q.Get("focus_type"), q.Get("focus"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "focus: "+err.Error())
		return
	}
	hops := clampKnowledgeInt(q.Get("hops"), knowledgeGraphDefaultHops, 1, knowledgeGraphMaxHops)
	edgeLimit := clampKnowledgeInt(q.Get("limit"), knowledgeGraphDefaultEdges, 1, knowledgeGraphMaxEdges)
	statuses := knowledgeStatusesFromQuery(q.Get("include_proposed") == "true")

	var minConfidence float32
	if v := strings.TrimSpace(q.Get("min_confidence")); v != "" {
		f, ferr := strconv.ParseFloat(v, 32)
		if ferr != nil || f < 0 || f > 1 {
			writeError(w, http.StatusBadRequest, "min_confidence must be in [0, 1]")
			return
		}
		minConfidence = float32(f)
	}
	var asOf pgtype.Timestamptz
	if v := strings.TrimSpace(q.Get("as_of")); v != "" {
		t, terr := time.Parse(time.RFC3339, v)
		if terr != nil {
			writeError(w, http.StatusBadRequest, "as_of must be RFC3339")
			return
		}
		asOf = pgtype.Timestamptz{Time: t, Valid: true}
	}

	focus := knowledgeEndpointKey{Type: focusType, ID: uuidToString(focusID)}
	edges, visited, _, truncated, err := h.knowledgeGraphExpand(
		r.Context(), wsUUID, []knowledgeEndpointKey{focus}, hops, statuses, asOf, minConfidence, edgeLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to expand knowledge graph")
		return
	}
	nodes, refs, err := h.hydrateKnowledgeGraph(r.Context(), wsUUID, visited)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hydrate knowledge graph")
		return
	}
	edgeResp := make([]KnowledgeEdgeResponse, 0, len(edges))
	for _, e := range edges {
		edgeResp = append(edgeResp, knowledgeEdgeToResponse(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"focus":     map[string]string{"type": focus.Type, "id": focus.ID},
		"nodes":     nodes,
		"refs":      refs,
		"edges":     edgeResp,
		"hops":      hops,
		"truncated": truncated,
	})
}

// GetKnowledgePath finds a connecting chain between two endpoints within
// max_hops: GET /api/knowledge/path?src=&src_type=&dst=&dst_type=&max_hops=&include_proposed=
// BFS with parent pointers over the same bounded frontier queries as
// /graph; the first hit is a shortest edge-count path.
func (h *Handler) GetKnowledgePath(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	q := r.URL.Query()
	srcType, srcID, err := h.resolveKnowledgeEndpoint(r.Context(), wsUUID, q.Get("src_type"), q.Get("src"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "src: "+err.Error())
		return
	}
	dstType, dstID, err := h.resolveKnowledgeEndpoint(r.Context(), wsUUID, q.Get("dst_type"), q.Get("dst"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "dst: "+err.Error())
		return
	}
	maxHops := clampKnowledgeInt(q.Get("max_hops"), knowledgeGraphMaxHops, 1, knowledgeGraphMaxHops)
	statuses := knowledgeStatusesFromQuery(q.Get("include_proposed") == "true")

	src := knowledgeEndpointKey{Type: srcType, ID: uuidToString(srcID)}
	dst := knowledgeEndpointKey{Type: dstType, ID: uuidToString(dstID)}
	if src == dst {
		writeError(w, http.StatusBadRequest, "src and dst must differ")
		return
	}

	edges, visited, parentEdge, truncated, err := h.knowledgeGraphExpand(
		r.Context(), wsUUID, []knowledgeEndpointKey{src}, maxHops, statuses, pgtype.Timestamptz{}, 0, knowledgeGraphMaxEdges)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search for path")
		return
	}
	if !visited[dst] {
		writeJSON(w, http.StatusOK, map[string]any{"found": false, "truncated": truncated})
		return
	}

	// Walk parent pointers dst → src collecting the connecting edges.
	pathEdges := make([]db.KnowledgeEdge, 0, maxHops)
	pathVisited := map[knowledgeEndpointKey]bool{src: true, dst: true}
	current := dst
	for current != src {
		edgeID, ok := parentEdge[current]
		if !ok {
			break
		}
		e := edges[edgeID]
		pathEdges = append(pathEdges, e)
		srcEp := knowledgeEndpointKey{Type: e.SrcType, ID: uuidToString(e.SrcID)}
		dstEp := knowledgeEndpointKey{Type: e.DstType, ID: uuidToString(e.DstID)}
		next := srcEp
		if srcEp == current {
			next = dstEp
		}
		pathVisited[next] = true
		current = next
	}
	// Reverse into src → dst order.
	for i, j := 0, len(pathEdges)-1; i < j; i, j = i+1, j-1 {
		pathEdges[i], pathEdges[j] = pathEdges[j], pathEdges[i]
	}

	nodes, refs, err := h.hydrateKnowledgeGraph(r.Context(), wsUUID, pathVisited)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hydrate path")
		return
	}
	edgeResp := make([]KnowledgeEdgeResponse, len(pathEdges))
	for i, e := range pathEdges {
		edgeResp[i] = knowledgeEdgeToResponse(e)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found":     true,
		"hops":      len(pathEdges),
		"edges":     edgeResp,
		"nodes":     nodes,
		"refs":      refs,
		"truncated": truncated,
	})
}
