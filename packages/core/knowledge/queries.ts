import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  KnowledgeGraphParams,
  KnowledgePathParams,
  ListKnowledgeEdgesResponse,
  ListKnowledgeNodesResponse,
} from "../types";

export const knowledgeKeys = {
  all: (wsId: string) => ["knowledge", wsId] as const,
  search: (wsId: string, q: string) =>
    [...knowledgeKeys.all(wsId), "search", q] as const,
  graph: (wsId: string, params: KnowledgeGraphParams) =>
    [...knowledgeKeys.all(wsId), "graph", params] as const,
  path: (wsId: string, params: KnowledgePathParams) =>
    [...knowledgeKeys.all(wsId), "path", params] as const,
  node: (wsId: string, idOrSlug: string) =>
    [...knowledgeKeys.all(wsId), "node", idOrSlug] as const,
  edge: (wsId: string, id: string) =>
    [...knowledgeKeys.all(wsId), "edge", id] as const,
  review: (wsId: string) => [...knowledgeKeys.all(wsId), "review"] as const,
  reviewNodes: (wsId: string) =>
    [...knowledgeKeys.review(wsId), "nodes"] as const,
  reviewEdges: (wsId: string) =>
    [...knowledgeKeys.review(wsId), "edges"] as const,
};

export function knowledgeSearchOptions(wsId: string, q: string, limit?: number) {
  return queryOptions({
    queryKey: knowledgeKeys.search(wsId, q),
    queryFn: ({ signal }) => api.searchKnowledge({ q, limit, signal }),
    enabled: q.trim().length > 0,
  });
}

export function knowledgeGraphOptions(wsId: string, params: KnowledgeGraphParams) {
  return queryOptions({
    queryKey: knowledgeKeys.graph(wsId, params),
    queryFn: () => api.getKnowledgeGraph(params),
    enabled: params.focus.length > 0,
  });
}

export function knowledgePathOptions(wsId: string, params: KnowledgePathParams) {
  return queryOptions({
    queryKey: knowledgeKeys.path(wsId, params),
    queryFn: () => api.getKnowledgePath(params),
    enabled: params.src.length > 0 && params.dst.length > 0,
  });
}

export function knowledgeNodeOptions(wsId: string, idOrSlug: string) {
  return queryOptions({
    queryKey: knowledgeKeys.node(wsId, idOrSlug),
    queryFn: () => api.getKnowledgeNode(idOrSlug),
    select: (data) => data.node,
    enabled: idOrSlug.length > 0,
  });
}

export function knowledgeEdgeOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: knowledgeKeys.edge(wsId, id),
    queryFn: () => api.getKnowledgeEdge(id),
    enabled: id.length > 0,
  });
}

/** Proposed nodes awaiting member review. */
export function knowledgeReviewNodesOptions(wsId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.reviewNodes(wsId),
    queryFn: () => api.listKnowledgeNodes({ status: "proposed", limit: 200 }),
  });
}

/** Proposed edges awaiting member review. */
export function knowledgeReviewEdgesOptions(wsId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.reviewEdges(wsId),
    queryFn: () => api.listKnowledgeEdges({ status: "proposed", limit: 200 }),
  });
}

/**
 * Approve/reject a proposed node. Optimistically drops the node from the
 * review list so the queue feels instant; rolls back on failure and
 * re-syncs the whole knowledge cache on settle (edges referencing the node
 * change render treatment with its status).
 */
export function useUpdateKnowledgeNodeStatus(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api.updateKnowledgeNode(id, { status }),
    onMutate: async ({ id }) => {
      await qc.cancelQueries({ queryKey: knowledgeKeys.reviewNodes(wsId) });
      const prev = qc.getQueryData<ListKnowledgeNodesResponse>(
        knowledgeKeys.reviewNodes(wsId),
      );
      qc.setQueryData<ListKnowledgeNodesResponse>(
        knowledgeKeys.reviewNodes(wsId),
        (old) =>
          old
            ? {
                ...old,
                nodes: old.nodes.filter((n) => n.id !== id),
                total: Math.max(0, old.total - 1),
              }
            : old,
      );
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) {
        qc.setQueryData(knowledgeKeys.reviewNodes(wsId), ctx.prev);
      }
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}

/** Approve/reject a proposed edge — same optimistic pattern as nodes. */
export function useUpdateKnowledgeEdgeStatus(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api.updateKnowledgeEdgeStatus(id, status),
    onMutate: async ({ id }) => {
      await qc.cancelQueries({ queryKey: knowledgeKeys.reviewEdges(wsId) });
      const prev = qc.getQueryData<ListKnowledgeEdgesResponse>(
        knowledgeKeys.reviewEdges(wsId),
      );
      qc.setQueryData<ListKnowledgeEdgesResponse>(
        knowledgeKeys.reviewEdges(wsId),
        (old) =>
          old
            ? {
                ...old,
                edges: old.edges.filter((e) => e.id !== id),
                total: Math.max(0, old.total - 1),
              }
            : old,
      );
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) {
        qc.setQueryData(knowledgeKeys.reviewEdges(wsId), ctx.prev);
      }
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}
