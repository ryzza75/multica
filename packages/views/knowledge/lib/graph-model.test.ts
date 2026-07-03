import { describe, expect, it } from "vitest";
import type {
  KnowledgeEdge,
  KnowledgeGraphResponse,
  KnowledgeNode,
} from "@multica/core/types";
import {
  buildGraphModel,
  KIND_COLORS,
  mergeGraphResponses,
  REF_COLOR,
} from "./graph-model";

function makeNode(overrides: Partial<KnowledgeNode> & { id: string }): KnowledgeNode {
  return {
    workspace_id: "ws-1",
    kind: "concept",
    slug: overrides.id,
    title: `Node ${overrides.id}`,
    aliases: [],
    summary: null,
    content: null,
    attrs: {},
    status: "confirmed",
    merged_into: null,
    created_by_type: "member",
    created_by_id: "u-1",
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    ...overrides,
  };
}

function makeEdge(
  overrides: Partial<KnowledgeEdge> & { id: string; src_id: string; dst_id: string },
): KnowledgeEdge {
  return {
    workspace_id: "ws-1",
    src_type: "node",
    dst_type: "node",
    predicate: "relates_to",
    confidence: 0.8,
    attrs: {},
    status: "confirmed",
    valid_from: null,
    valid_until: null,
    superseded_by: null,
    last_affirmed_at: "2026-06-01T00:00:00Z",
    created_by_type: "agent",
    created_by_id: "a-1",
    created_at: "2026-06-01T00:00:00Z",
    ...overrides,
  };
}

describe("buildGraphModel", () => {
  it("maps nodes with kind colors and refs with the ref color", () => {
    const model = buildGraphModel(
      [makeNode({ id: "n1", kind: "person" })],
      [{ type: "issue", id: "i1" }],
      [makeEdge({ id: "e1", src_id: "n1", dst_id: "i1", dst_type: "issue" })],
    );

    const person = model.nodes.find((n) => n.key === "n1");
    const ref = model.nodes.find((n) => n.key === "i1");
    expect(person?.color).toBe(KIND_COLORS.person);
    expect(person?.isRef).toBe(false);
    expect(ref?.color).toBe(REF_COLOR);
    expect(ref?.isRef).toBe(true);
    expect(ref?.kind).toBe("issue");
    expect(model.edges).toHaveLength(1);
  });

  it("falls back to the ref color for unknown kinds", () => {
    const model = buildGraphModel(
      [makeNode({ id: "n1", kind: "future_kind" })],
      [],
      [],
    );
    expect(model.nodes[0]?.color).toBe(REF_COLOR);
  });

  it("scales node size by degree", () => {
    const nodes = [
      makeNode({ id: "hub" }),
      makeNode({ id: "a" }),
      makeNode({ id: "b" }),
      makeNode({ id: "isolated" }),
    ];
    const edges = [
      makeEdge({ id: "e1", src_id: "hub", dst_id: "a" }),
      makeEdge({ id: "e2", src_id: "hub", dst_id: "b" }),
    ];
    const model = buildGraphModel(nodes, [], edges, { minSize: 4, maxSize: 10 });

    const size = (key: string) => model.nodes.find((n) => n.key === key)?.size;
    expect(size("hub")).toBe(10);
    expect(size("isolated")).toBe(4);
    expect(size("a")).toBeGreaterThan(4);
    expect(size("a")).toBeLessThan(10);
  });

  it("flags proposed edges as dashed and proposed nodes as faded", () => {
    const model = buildGraphModel(
      [makeNode({ id: "n1", status: "proposed" }), makeNode({ id: "n2" })],
      [],
      [makeEdge({ id: "e1", src_id: "n1", dst_id: "n2", status: "proposed" })],
    );
    expect(model.nodes.find((n) => n.key === "n1")?.faded).toBe(true);
    expect(model.nodes.find((n) => n.key === "n2")?.faded).toBe(false);
    expect(model.edges[0]?.dashed).toBe(true);
  });

  it("drops edges whose endpoints are missing", () => {
    const model = buildGraphModel(
      [makeNode({ id: "n1" })],
      [],
      [makeEdge({ id: "e1", src_id: "n1", dst_id: "gone" })],
    );
    expect(model.edges).toHaveLength(0);
  });
});

describe("mergeGraphResponses", () => {
  const base: KnowledgeGraphResponse = {
    focus: { type: "node", id: "n1" },
    nodes: [makeNode({ id: "n1" })],
    refs: [{ type: "issue", id: "i1" }],
    edges: [makeEdge({ id: "e1", src_id: "n1", dst_id: "i1", dst_type: "issue" })],
    hops: 2,
    truncated: false,
  };

  it("dedupes nodes, refs, and edges by identity", () => {
    const next: KnowledgeGraphResponse = {
      focus: { type: "node", id: "n2" },
      nodes: [makeNode({ id: "n1", title: "Fresher" }), makeNode({ id: "n2" })],
      refs: [{ type: "issue", id: "i1" }, { type: "agent", id: "a1" }],
      edges: [
        makeEdge({ id: "e1", src_id: "n1", dst_id: "i1", dst_type: "issue" }),
        makeEdge({ id: "e2", src_id: "n1", dst_id: "n2" }),
      ],
      hops: 1,
      truncated: true,
    };
    const merged = mergeGraphResponses(base, next);

    expect(merged.nodes.map((n) => n.id).sort()).toEqual(["n1", "n2"]);
    // Later response wins on node hydration.
    expect(merged.nodes.find((n) => n.id === "n1")?.title).toBe("Fresher");
    expect(merged.refs).toHaveLength(2);
    expect(merged.edges.map((e) => e.id).sort()).toEqual(["e1", "e2"]);
    // Original focus is preserved; truncation is sticky.
    expect(merged.focus).toEqual({ type: "node", id: "n1" });
    expect(merged.truncated).toBe(true);
    expect(merged.hops).toBe(2);
  });
});
