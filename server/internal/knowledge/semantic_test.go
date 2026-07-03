package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRRFMerge(t *testing.T) {
	// "b" is ranked well by both arms and must beat the single-arm
	// leaders even though it tops neither list.
	fused := RRFMerge(
		[]string{"a", "b", "c"},
		[]string{"d", "b", "e"},
	)
	if fused[0] != "b" {
		t.Fatalf("expected b first (best combined rank), got %v", fused)
	}
	if len(fused) != 5 {
		t.Fatalf("expected 5 unique ids, got %v", fused)
	}

	// Ties resolve by first appearance so output is deterministic.
	tied := RRFMerge([]string{"x"}, []string{"y"})
	if tied[0] != "x" || tied[1] != "y" {
		t.Fatalf("tie should keep first-seen order, got %v", tied)
	}

	if got := RRFMerge(nil, nil); len(got) != 0 {
		t.Fatalf("empty rankings should fuse to empty, got %v", got)
	}
}

func TestVectorLiteral(t *testing.T) {
	got := vectorLiteral([]float32{0.5, -1, 0})
	if got != "[0.5,-1,0]" {
		t.Fatalf("unexpected literal %q", got)
	}
}

func TestConfigEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("empty config must be disabled")
	}
	if !(Config{APIKey: "sk-x"}).Enabled() {
		t.Fatal("api key alone should enable (OpenAI default base)")
	}
	if !(Config{BaseURL: "http://localhost:11434/v1"}).Enabled() {
		t.Fatal("explicit base URL alone should enable (keyless local provider)")
	}
}

// fakeEmbedder builds an OpenAI-compatible /embeddings server whose vector
// depends on keyword presence, so similarity is controllable from tests.
func fakeEmbedder(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		type datum struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		// Return data out of order to prove index-based reassembly.
		data := make([]datum, 0, len(req.Input))
		for i := len(req.Input) - 1; i >= 0; i-- {
			vec := make([]float32, dim)
			if strings.Contains(strings.ToLower(req.Input[i]), "zebra") {
				vec[0] = 1
			} else {
				vec[1] = 1
			}
			data = append(data, datum{Index: i, Embedding: vec})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestEmbedOrderAndDimValidation(t *testing.T) {
	srv := fakeEmbedder(t, EmbeddingDim)
	defer srv.Close()

	svc := New(Config{BaseURL: srv.URL, Model: "test-model"}, nil)
	vecs, err := svc.Embed(context.Background(), []string{"zebra one", "plain two"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Fatalf("vectors not reassembled by index: %v %v", vecs[0][:2], vecs[1][:2])
	}

	// Wrong dimensionality must fail closed, never write garbage.
	bad := fakeEmbedder(t, 8)
	defer bad.Close()
	svc = New(Config{BaseURL: bad.URL, Model: "tiny-model"}, nil)
	if _, err := svc.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected dimension-mismatch error")
	}
}

func TestEmbedDisabled(t *testing.T) {
	svc := New(Config{}, nil)
	if _, err := svc.Embed(context.Background(), []string{"x"}); err != ErrSemanticUnavailable {
		t.Fatalf("expected ErrSemanticUnavailable, got %v", err)
	}
	if _, err := svc.SemanticSearch(context.Background(), "ws", "q", 5); err != ErrSemanticUnavailable {
		t.Fatalf("expected ErrSemanticUnavailable from search, got %v", err)
	}
}

func TestEmbeddingTextBounded(t *testing.T) {
	n := staleNode{Kind: "concept", Title: "T", Content: strings.Repeat("x", 20000)}
	if len(n.embeddingText()) > 7000 {
		t.Fatalf("embedding text not bounded: %d chars", len(n.embeddingText()))
	}
}
