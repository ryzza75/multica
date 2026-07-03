import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { KnowledgeEdge, KnowledgeNode } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { KnowledgeReview } from "./knowledge-review";

const nodeFixture = (over: Partial<KnowledgeNode>): KnowledgeNode => ({
  id: "node-1",
  workspace_id: "workspace-1",
  kind: "concept",
  slug: "test-concept",
  title: "Test Concept",
  aliases: [],
  summary: null,
  content: null,
  attrs: {},
  status: "proposed",
  merged_into: null,
  created_by_type: "agent",
  created_by_id: "agent-1",
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
  ...over,
});

const edgeFixture = (over: Partial<KnowledgeEdge>): KnowledgeEdge => ({
  id: "edge-1",
  workspace_id: "workspace-1",
  src_type: "node",
  src_id: "node-1",
  dst_type: "node",
  dst_id: "node-2",
  predicate: "related_to",
  confidence: 0.6,
  attrs: {},
  status: "proposed",
  valid_from: null,
  valid_until: null,
  superseded_by: null,
  last_affirmed_at: "2026-07-01T00:00:00Z",
  created_by_type: "agent",
  created_by_id: "agent-1",
  created_at: "2026-07-01T00:00:00Z",
  ...over,
});

const mocks = vi.hoisted(() => ({
  reviewNodes: [] as unknown[],
  reviewEdges: [] as unknown[],
  nodeStatusMutate: vi.fn(),
  edgeStatusMutate: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: readonly unknown[] }) => {
    // knowledgeKeys.* shapes: ["knowledge", wsId, "review", "nodes"|"edges"]
    // and ["knowledge", wsId, "node", id] for endpoint labels.
    const key = options.queryKey ?? [];
    if (key[2] === "review" && key[3] === "nodes") {
      return { data: { nodes: mocks.reviewNodes, total: mocks.reviewNodes.length }, isLoading: false };
    }
    if (key[2] === "review" && key[3] === "edges") {
      return { data: { edges: mocks.reviewEdges, total: mocks.reviewEdges.length }, isLoading: false };
    }
    return { data: undefined, isLoading: false };
  },
}));

vi.mock("@multica/core/knowledge", () => ({
  knowledgeKeys: {
    reviewNodes: (wsId: string) => ["knowledge", wsId, "review", "nodes"] as const,
    reviewEdges: (wsId: string) => ["knowledge", wsId, "review", "edges"] as const,
  },
  knowledgeReviewNodesOptions: (wsId: string) => ({
    queryKey: ["knowledge", wsId, "review", "nodes"],
  }),
  knowledgeReviewEdgesOptions: (wsId: string) => ({
    queryKey: ["knowledge", wsId, "review", "edges"],
  }),
  knowledgeNodeOptions: (wsId: string, id: string) => ({
    queryKey: ["knowledge", wsId, "node", id],
  }),
  useUpdateKnowledgeNodeStatus: () => ({ mutate: mocks.nodeStatusMutate }),
  useUpdateKnowledgeEdgeStatus: () => ({ mutate: mocks.edgeStatusMutate }),
}));

const navigationAdapter: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/acme/knowledge",
  searchParams: new URLSearchParams(),
  getShareableUrl: (path) => path,
};

function renderReview(onInspectNode = vi.fn()) {
  return renderWithI18n(
    <NavigationProvider value={navigationAdapter}>
      <KnowledgeReview wsId="workspace-1" onInspectNode={onInspectNode} />
    </NavigationProvider>,
  );
}

describe("KnowledgeReview", () => {
  beforeEach(() => {
    mocks.reviewNodes = [];
    mocks.reviewEdges = [];
    mocks.nodeStatusMutate.mockClear();
    mocks.edgeStatusMutate.mockClear();
  });

  it("shows the empty state when nothing is proposed", () => {
    renderReview();
    expect(screen.getByText("Nothing is waiting for review")).toBeInTheDocument();
  });

  it("approves a proposed node as confirmed", async () => {
    mocks.reviewNodes = [nodeFixture({ title: "Quantum Networks" })];
    renderReview();
    expect(screen.getByText("Quantum Networks")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /approve/i }));
    expect(mocks.nodeStatusMutate).toHaveBeenCalledWith({ id: "node-1", status: "confirmed" });
  });

  it("rejects a proposed edge", async () => {
    mocks.reviewEdges = [edgeFixture({})];
    renderReview();
    expect(screen.getByText("related_to")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /reject/i }));
    expect(mocks.edgeStatusMutate).toHaveBeenCalledWith({ id: "edge-1", status: "rejected" });
  });

  it("inspecting a proposed node hands its id to the explore view", async () => {
    const onInspectNode = vi.fn();
    mocks.reviewNodes = [nodeFixture({})];
    renderReview(onInspectNode);
    await userEvent.click(screen.getByText("Test Concept"));
    expect(onInspectNode).toHaveBeenCalledWith("node-1");
  });
});
