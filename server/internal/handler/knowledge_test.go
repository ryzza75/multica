package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func cleanupKnowledgeRows(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		// Evidence and revisions cascade from edges/nodes; edges reference
		// each other via superseded_by (SET NULL), so edges go first.
		testPool.Exec(ctx, `DELETE FROM knowledge_edge WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM knowledge_source WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM knowledge_node WHERE workspace_id = $1`, testWorkspaceID)
	})
}

func createKnowledgeNodeForTest(t *testing.T, kind, title string) KnowledgeNodeResponse {
	t.Helper()
	req := newRequest("POST", "/api/knowledge/nodes", map[string]any{
		"kind": kind, "title": title, "confirm_new": true,
	})
	rec := httptest.NewRecorder()
	testHandler.CreateKnowledgeNode(rec, req)
	if rec.Code != 201 {
		t.Fatalf("create node %q: status %d body %s", title, rec.Code, rec.Body.String())
	}
	var resp struct {
		Node KnowledgeNodeResponse `json:"node"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode node response: %v", err)
	}
	return resp.Node
}

func addKnowledgeEdgeForTest(t *testing.T, body map[string]any) (int, map[string]json.RawMessage) {
	t.Helper()
	req := newRequest("POST", "/api/knowledge/edges", body)
	rec := httptest.NewRecorder()
	testHandler.CreateKnowledgeEdge(rec, req)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode edge response (status %d): %v body=%s", rec.Code, err, rec.Body.String())
	}
	return rec.Code, raw
}

func TestKnowledgeNodeDedupAndLifecycle(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupKnowledgeRows(t)

	// Create.
	node := createKnowledgeNodeForTest(t, "person", "Ada Lovelace")
	if node.Status != "confirmed" {
		t.Fatalf("member-created node should be confirmed, got %q", node.Status)
	}
	if node.Slug != "ada-lovelace" {
		t.Fatalf("expected slug ada-lovelace, got %q", node.Slug)
	}

	// Duplicate title without confirm_new → 409 with candidates.
	req := newRequest("POST", "/api/knowledge/nodes", map[string]any{
		"kind": "person", "title": "Ada Lovelace",
	})
	rec := httptest.NewRecorder()
	testHandler.CreateKnowledgeNode(rec, req)
	if rec.Code != 409 {
		t.Fatalf("duplicate create should 409, got %d: %s", rec.Code, rec.Body.String())
	}
	var conflict struct {
		Candidates []KnowledgeNodeResponse `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil || len(conflict.Candidates) == 0 {
		t.Fatalf("409 should carry candidates: %s", rec.Body.String())
	}

	// Get by slug.
	req = withURLParam(newRequest("GET", "/api/knowledge/nodes/ada-lovelace", nil), "id", "ada-lovelace")
	rec = httptest.NewRecorder()
	testHandler.GetKnowledgeNode(rec, req)
	if rec.Code != 200 {
		t.Fatalf("get by slug: status %d", rec.Code)
	}

	// Update content → revision recorded.
	req = withURLParam(newRequest("PUT", "/api/knowledge/nodes/"+node.ID, map[string]any{
		"content": "# Ada\nFirst programmer.",
	}), "id", node.ID)
	rec = httptest.NewRecorder()
	testHandler.UpdateKnowledgeNode(rec, req)
	if rec.Code != 200 {
		t.Fatalf("update node: status %d body %s", rec.Code, rec.Body.String())
	}
	req = withURLParam(newRequest("GET", "/api/knowledge/nodes/"+node.ID+"/revisions", nil), "id", node.ID)
	rec = httptest.NewRecorder()
	testHandler.ListKnowledgeNodeRevisionsHandler(rec, req)
	var revisions struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &revisions); err != nil || revisions.Total < 1 {
		t.Fatalf("expected at least one revision, got %s", rec.Body.String())
	}

	// Search finds it by alias-ish partial title.
	req = newRequest("GET", "/api/knowledge/search?q=Lovelace", nil)
	rec = httptest.NewRecorder()
	testHandler.SearchKnowledgeNodes(rec, req)
	var search struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &search); err != nil || search.Total < 1 {
		t.Fatalf("search should find the node: %s", rec.Body.String())
	}
}

func TestKnowledgeEdgeAffirmAndSupersede(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupKnowledgeRows(t)

	person := createKnowledgeNodeForTest(t, "person", "Edge Test Person")
	orgA := createKnowledgeNodeForTest(t, "organization", "Edge Test Org A")
	orgB := createKnowledgeNodeForTest(t, "organization", "Edge Test Org B")

	// First assertion with provenance.
	code, raw := addKnowledgeEdgeForTest(t, map[string]any{
		"src_id": person.Slug, "dst_id": orgA.Slug, "predicate": "works_at",
		"source_url": "https://example.com/bio",
	})
	if code != 201 {
		t.Fatalf("first edge create should 201, got %d", code)
	}
	var edge KnowledgeEdgeResponse
	if err := json.Unmarshal(raw["edge"], &edge); err != nil {
		t.Fatalf("decode edge: %v", err)
	}

	// Identical re-assertion is affirmed, not duplicated.
	code, raw = addKnowledgeEdgeForTest(t, map[string]any{
		"src_id": person.Slug, "dst_id": orgA.Slug, "predicate": "works_at",
		"source_url": "https://example.com/second-source",
	})
	if code != 200 {
		t.Fatalf("re-assertion should 200 (affirm), got %d", code)
	}
	var affirmed bool
	if err := json.Unmarshal(raw["affirmed"], &affirmed); err != nil || !affirmed {
		t.Fatalf("expected affirmed=true, got %v", raw)
	}
	var affirmedEdge KnowledgeEdgeResponse
	if err := json.Unmarshal(raw["edge"], &affirmedEdge); err != nil {
		t.Fatalf("decode affirmed edge: %v", err)
	}
	if affirmedEdge.ID != edge.ID {
		t.Fatalf("affirmation must not create a new edge: %s vs %s", affirmedEdge.ID, edge.ID)
	}
	if affirmedEdge.Confidence <= edge.Confidence {
		t.Fatalf("second supporting source should raise confidence: %v -> %v", edge.Confidence, affirmedEdge.Confidence)
	}

	// Functional conflict without supersede → 409 with the live fact.
	code, raw = addKnowledgeEdgeForTest(t, map[string]any{
		"src_id": person.Slug, "dst_id": orgB.Slug, "predicate": "works_at",
	})
	if code != 409 {
		t.Fatalf("functional conflict should 409, got %d: %v", code, raw)
	}

	// With supersede=true the old fact closes, pointing at the new one.
	code, raw = addKnowledgeEdgeForTest(t, map[string]any{
		"src_id": person.Slug, "dst_id": orgB.Slug, "predicate": "works_at",
		"supersede": true,
	})
	if code != 201 {
		t.Fatalf("supersede create should 201, got %d: %v", code, raw)
	}
	var newEdge KnowledgeEdgeResponse
	if err := json.Unmarshal(raw["edge"], &newEdge); err != nil {
		t.Fatalf("decode superseding edge: %v", err)
	}
	var superseded []string
	if err := json.Unmarshal(raw["superseded"], &superseded); err != nil || len(superseded) != 1 || superseded[0] != edge.ID {
		t.Fatalf("expected superseded=[%s], got %v", edge.ID, raw)
	}
	req := withURLParam(newRequest("GET", "/api/knowledge/edges/"+edge.ID, nil), "id", edge.ID)
	rec := httptest.NewRecorder()
	testHandler.GetKnowledgeEdge(rec, req)
	var closedResp struct {
		Edge KnowledgeEdgeResponse `json:"edge"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &closedResp); err != nil {
		t.Fatalf("decode closed edge: %v", err)
	}
	if closedResp.Edge.ValidUntil == nil || closedResp.Edge.SupersededBy == nil || *closedResp.Edge.SupersededBy != newEdge.ID {
		t.Fatalf("old fact should be closed and superseded by %s: %+v", newEdge.ID, closedResp.Edge)
	}

	// Contradicting evidence lowers confidence.
	before := newEdge.Confidence
	req = withURLParam(newRequest("POST", "/api/knowledge/edges/"+newEdge.ID+"/evidence", map[string]any{
		"source_url": "https://example.com/rebuttal", "stance": "contradicts",
	}), "id", newEdge.ID)
	rec = httptest.NewRecorder()
	testHandler.AddKnowledgeEvidence(rec, req)
	if rec.Code != 200 {
		t.Fatalf("add evidence: status %d body %s", rec.Code, rec.Body.String())
	}
	var evidenceResp struct {
		Edge KnowledgeEdgeResponse `json:"edge"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &evidenceResp); err != nil {
		t.Fatalf("decode evidence response: %v", err)
	}
	if evidenceResp.Edge.Confidence >= before {
		t.Fatalf("contradicting evidence should lower confidence: %v -> %v", before, evidenceResp.Edge.Confidence)
	}
}

func TestKnowledgeGraphExpansionAndPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupKnowledgeRows(t)

	// Chain: a -related_to-> b -related_to-> c -related_to-> d
	names := []string{"Graph Node A", "Graph Node B", "Graph Node C", "Graph Node D"}
	nodes := make([]KnowledgeNodeResponse, len(names))
	for i, n := range names {
		nodes[i] = createKnowledgeNodeForTest(t, "concept", n)
	}
	for i := 0; i < len(nodes)-1; i++ {
		code, raw := addKnowledgeEdgeForTest(t, map[string]any{
			"src_id": nodes[i].ID, "dst_id": nodes[i+1].ID, "predicate": "related_to",
		})
		if code != 201 {
			t.Fatalf("edge %d create: status %d %v", i, code, raw)
		}
	}

	// hops=2 from A sees A-B and B-C but not C-D.
	req := newRequest("GET", fmt.Sprintf("/api/knowledge/graph?focus=%s&hops=2", nodes[0].Slug), nil)
	rec := httptest.NewRecorder()
	testHandler.GetKnowledgeGraph(rec, req)
	if rec.Code != 200 {
		t.Fatalf("graph: status %d body %s", rec.Code, rec.Body.String())
	}
	var graph struct {
		Nodes []KnowledgeNodeResponse `json:"nodes"`
		Edges []KnowledgeEdgeResponse `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &graph); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(graph.Edges) != 2 {
		t.Fatalf("hops=2 should return 2 edges, got %d", len(graph.Edges))
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("hops=2 should hydrate 3 nodes, got %d", len(graph.Nodes))
	}

	// Path A → D crosses all three edges.
	req = newRequest("GET", fmt.Sprintf("/api/knowledge/path?src=%s&dst=%s", nodes[0].Slug, nodes[3].Slug), nil)
	rec = httptest.NewRecorder()
	testHandler.GetKnowledgePath(rec, req)
	if rec.Code != 200 {
		t.Fatalf("path: status %d body %s", rec.Code, rec.Body.String())
	}
	var path struct {
		Found bool                    `json:"found"`
		Hops  int                     `json:"hops"`
		Edges []KnowledgeEdgeResponse `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &path); err != nil {
		t.Fatalf("decode path: %v", err)
	}
	if !path.Found || path.Hops != 3 || len(path.Edges) != 3 {
		t.Fatalf("expected found path with 3 hops, got %+v", path)
	}

	// Unreachable target reports found=false.
	lonely := createKnowledgeNodeForTest(t, "concept", "Graph Lonely Node")
	req = newRequest("GET", fmt.Sprintf("/api/knowledge/path?src=%s&dst=%s", nodes[0].Slug, lonely.Slug), nil)
	rec = httptest.NewRecorder()
	testHandler.GetKnowledgePath(rec, req)
	var lonelyPath struct {
		Found bool `json:"found"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lonelyPath); err != nil || lonelyPath.Found {
		t.Fatalf("disconnected path should be found=false: %s", rec.Body.String())
	}
}

func TestKnowledgeNodeMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	cleanupKnowledgeRows(t)

	winner := createKnowledgeNodeForTest(t, "organization", "Merge Winner Org")
	loser := createKnowledgeNodeForTest(t, "organization", "Merge Loser Org")
	other := createKnowledgeNodeForTest(t, "person", "Merge Person")

	code, _ := addKnowledgeEdgeForTest(t, map[string]any{
		"src_id": other.ID, "dst_id": loser.ID, "predicate": "member_of",
	})
	if code != 201 {
		t.Fatalf("setup edge: status %d", code)
	}

	req := withURLParam(newRequest("POST", "/api/knowledge/nodes/"+loser.ID+"/merge", map[string]any{
		"into": winner.Slug,
	}), "id", loser.ID)
	rec := httptest.NewRecorder()
	testHandler.MergeKnowledgeNode(rec, req)
	if rec.Code != 200 {
		t.Fatalf("merge: status %d body %s", rec.Code, rec.Body.String())
	}

	// Old reference resolves to the winner via the redirect.
	req = withURLParam(newRequest("GET", "/api/knowledge/nodes/"+loser.ID, nil), "id", loser.ID)
	rec = httptest.NewRecorder()
	testHandler.GetKnowledgeNode(rec, req)
	var resolved struct {
		Node KnowledgeNodeResponse `json:"node"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode redirect: %v", err)
	}
	if resolved.Node.ID != winner.ID {
		t.Fatalf("merged node should resolve to winner %s, got %s", winner.ID, resolved.Node.ID)
	}

	// The edge moved to the winner.
	req = newRequest("GET", "/api/knowledge/edges?endpoint_id="+winner.ID, nil)
	rec = httptest.NewRecorder()
	testHandler.ListKnowledgeEdges(rec, req)
	var edges struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &edges); err != nil || edges.Total != 1 {
		t.Fatalf("winner should hold the moved edge: %s", rec.Body.String())
	}
}
