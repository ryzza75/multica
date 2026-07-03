import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import {
  EMPTY_KNOWLEDGE_GRAPH_RESPONSE,
  EMPTY_KNOWLEDGE_PATH_RESPONSE,
  EMPTY_SEARCH_KNOWLEDGE_NODES_RESPONSE,
} from "./schemas";

function stubFetchJson(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(typeof body === "string" ? body : JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

// Knowledge endpoints must survive backend response drift: malformed
// payloads fall back to the EMPTY_* constants instead of crashing, and
// unknown extra fields pass through (.loose()).
describe("knowledge API schema fallbacks", () => {
  it("searchKnowledge falls back on malformed body", async () => {
    stubFetchJson({ nodes: "not-an-array" });
    const client = new ApiClient("https://api.example.test");
    const res = await client.searchKnowledge({ q: "x" });
    expect(res).toEqual(EMPTY_SEARCH_KNOWLEDGE_NODES_RESPONSE);
  });

  it("searchKnowledge passes through unknown fields and enum drift", async () => {
    stubFetchJson({
      nodes: [
        {
          id: "n1",
          workspace_id: "w1",
          kind: "some-future-kind",
          slug: "s",
          title: "T",
          aliases: [],
          summary: null,
          content: null,
          attrs: {},
          status: "brand-new-status",
          merged_into: null,
          created_by_type: "agent",
          created_by_id: "a1",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          server_only_field: 42,
        },
      ],
      total: 1,
      semantic: true,
      future_top_level: "ok",
    });
    const client = new ApiClient("https://api.example.test");
    const res = await client.searchKnowledge({ q: "x" });
    expect(res.nodes[0]?.kind).toBe("some-future-kind");
    expect(res.nodes[0]?.status).toBe("brand-new-status");
    expect(res.semantic).toBe(true);
  });

  it("getKnowledgeGraph falls back on malformed body", async () => {
    stubFetchJson([1, 2, 3]);
    const client = new ApiClient("https://api.example.test");
    const res = await client.getKnowledgeGraph({ focus: "n1" });
    expect(res).toEqual(EMPTY_KNOWLEDGE_GRAPH_RESPONSE);
  });

  it("getKnowledgePath tolerates the found=false minimal shape", async () => {
    stubFetchJson({ found: false, truncated: true });
    const client = new ApiClient("https://api.example.test");
    const res = await client.getKnowledgePath({ src: "a", dst: "b" });
    expect(res.found).toBe(false);
    expect(res.edges).toEqual([]);
    expect(res.nodes).toEqual([]);
  });

  it("getKnowledgePath falls back on malformed body", async () => {
    stubFetchJson({ found: "yes", edges: 7 });
    const client = new ApiClient("https://api.example.test");
    const res = await client.getKnowledgePath({ src: "a", dst: "b" });
    expect(res).toEqual(EMPTY_KNOWLEDGE_PATH_RESPONSE);
  });
});
