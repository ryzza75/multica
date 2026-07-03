import type {
  KnowledgeEdge,
  KnowledgeGraphResponse,
  KnowledgeNode,
  KnowledgeRef,
} from "@multica/core/types";

// Pure graph-shaping helpers for the knowledge canvas. No React, no sigma —
// this module turns API responses into a renderer-agnostic model so it can
// be unit-tested without a DOM.

/**
 * Graph data palette, keyed by node kind. These are data-visualization
 * colors (like chart series), not UI chrome, so hex constants are the
 * correct tool — semantic Tailwind tokens don't reach into canvas
 * rendering. Hues are spread for colorblind-reasonable separation and
 * chosen to read on both light and dark canvas backgrounds.
 */
export const KIND_COLORS: Record<string, string> = {
  person: "#4c8df6",
  organization: "#8a5cf6",
  brand: "#d6539b",
  concept: "#12a594",
  idea: "#f0a83a",
  claim: "#e5484d",
  event: "#e5793a",
  work: "#3ca6c4",
  technology: "#6e78e0",
  market: "#7fb339",
  place: "#2f9e68",
  note: "#9b9a96",
};

/** Fallback for unknown node kinds and non-node refs (issue/project/...). */
export const REF_COLOR = "#8d8d86";

export interface GraphModelNode {
  key: string;
  label: string;
  /** Node kind, or the ref's endpoint type ("issue", "project", ...). */
  kind: string;
  status: string;
  size: number;
  color: string;
  /** True for non-node endpoints rendered as generic entity chips. */
  isRef: boolean;
  /** Proposed material renders faded until a member confirms it. */
  faded: boolean;
}

export interface GraphModelEdge {
  key: string;
  source: string;
  target: string;
  predicate: string;
  confidence: number;
  status: string;
  /** Proposed edges render dashed at lowered opacity. */
  dashed: boolean;
}

export interface GraphModel {
  nodes: GraphModelNode[];
  edges: GraphModelEdge[];
}

export interface BuildGraphModelOptions {
  /** Node size range in px; degree scales linearly between the two. */
  minSize?: number;
  maxSize?: number;
}

const DEFAULT_MIN_SIZE = 5;
const DEFAULT_MAX_SIZE = 14;

function refLabel(ref: KnowledgeRef): string {
  return `${ref.type} ${ref.id.slice(0, 8)}`;
}

/**
 * Shape one graph/path response into the canvas model. Edges whose
 * endpoints were dropped server-side (dangling refs) are excluded so the
 * renderer never sees a half-edge.
 */
export function buildGraphModel(
  nodes: KnowledgeNode[],
  refs: KnowledgeRef[],
  edges: KnowledgeEdge[],
  opts: BuildGraphModelOptions = {},
): GraphModel {
  const minSize = opts.minSize ?? DEFAULT_MIN_SIZE;
  const maxSize = opts.maxSize ?? DEFAULT_MAX_SIZE;

  const keys = new Set<string>();
  for (const n of nodes) keys.add(n.id);
  for (const r of refs) keys.add(r.id);

  const degree = new Map<string, number>();
  const modelEdges: GraphModelEdge[] = [];
  for (const e of edges) {
    if (!keys.has(e.src_id) || !keys.has(e.dst_id)) continue;
    degree.set(e.src_id, (degree.get(e.src_id) ?? 0) + 1);
    degree.set(e.dst_id, (degree.get(e.dst_id) ?? 0) + 1);
    modelEdges.push({
      key: e.id,
      source: e.src_id,
      target: e.dst_id,
      predicate: e.predicate,
      confidence: e.confidence,
      status: e.status,
      dashed: e.status === "proposed",
    });
  }

  const maxDegree = Math.max(1, ...degree.values());
  const sizeFor = (key: string): number =>
    minSize + ((degree.get(key) ?? 0) / maxDegree) * (maxSize - minSize);

  const modelNodes: GraphModelNode[] = [];
  for (const n of nodes) {
    modelNodes.push({
      key: n.id,
      label: n.title,
      kind: n.kind,
      status: n.status,
      size: sizeFor(n.id),
      color: KIND_COLORS[n.kind] ?? REF_COLOR,
      isRef: false,
      faded: n.status === "proposed",
    });
  }
  for (const r of refs) {
    if (nodes.some((n) => n.id === r.id)) continue;
    modelNodes.push({
      key: r.id,
      label: refLabel(r),
      kind: r.type,
      status: "",
      size: sizeFor(r.id),
      color: REF_COLOR,
      isRef: true,
      faded: false,
    });
  }

  return { nodes: modelNodes, edges: modelEdges };
}

/**
 * Merge an expansion response into an accumulated one (double-click on a
 * node loads its neighborhood and grows the canvas in place). Nodes are
 * replaced by id so fresher hydration wins; refs/edges dedupe by identity.
 */
export function mergeGraphResponses(
  prev: KnowledgeGraphResponse,
  next: KnowledgeGraphResponse,
): KnowledgeGraphResponse {
  const nodesById = new Map<string, KnowledgeNode>();
  for (const n of prev.nodes) nodesById.set(n.id, n);
  for (const n of next.nodes) nodesById.set(n.id, n);

  const refKeys = new Set<string>();
  const refs: KnowledgeRef[] = [];
  for (const r of [...prev.refs, ...next.refs]) {
    const key = `${r.type}:${r.id}`;
    if (refKeys.has(key)) continue;
    refKeys.add(key);
    refs.push(r);
  }

  const edgesById = new Map<string, KnowledgeEdge>();
  for (const e of prev.edges) edgesById.set(e.id, e);
  for (const e of next.edges) edgesById.set(e.id, e);

  return {
    focus: prev.focus ?? next.focus,
    nodes: [...nodesById.values()],
    refs,
    edges: [...edgesById.values()],
    hops: Math.max(prev.hops, next.hops),
    truncated: prev.truncated || next.truncated,
  };
}
