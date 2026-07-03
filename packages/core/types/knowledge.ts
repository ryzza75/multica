// Knowledge graph domain types (server/internal/handler/knowledge*.go).
// Status/kind fields are typed as unions for call-site ergonomics, but the
// API layer parses them leniently (z.string()) so unknown server values
// degrade instead of crashing — see CLAUDE.md "API Compatibility".

export type KnowledgeStatus = "proposed" | "confirmed" | "rejected";

export type KnowledgeNodeKind =
  | "person"
  | "organization"
  | "brand"
  | "concept"
  | "idea"
  | "claim"
  | "event"
  | "work"
  | "technology"
  | "market"
  | "place"
  | "note";

/** Polymorphic edge endpoint discriminator. Non-node types are workspace
 *  entities the UI resolves through its own queries (rendered as refs). */
export type KnowledgeEndpointType = "node" | "issue" | "project" | "agent" | "member";

export interface KnowledgeNode {
  id: string;
  workspace_id: string;
  kind: string;
  slug: string;
  title: string;
  aliases: string[];
  summary: string | null;
  /** Markdown body. */
  content: string | null;
  attrs: Record<string, unknown>;
  status: string;
  merged_into: string | null;
  created_by_type: string;
  created_by_id: string;
  created_at: string;
  updated_at: string;
}

export interface KnowledgeEdge {
  id: string;
  workspace_id: string;
  src_type: string;
  src_id: string;
  dst_type: string;
  dst_id: string;
  predicate: string;
  confidence: number;
  attrs: Record<string, unknown>;
  status: string;
  valid_from: string | null;
  valid_until: string | null;
  superseded_by: string | null;
  last_affirmed_at: string;
  created_by_type: string;
  created_by_id: string;
  created_at: string;
}

/** Non-node endpoint reachable from the graph (issue/project/agent/member).
 *  The UI renders these as generic entity chips labeled by type. */
export interface KnowledgeRef {
  type: string;
  id: string;
}

export interface KnowledgeSource {
  id: string;
  source_type: string;
  source_ref: unknown;
  title: string | null;
  content: string | null;
  created_at: string;
}

export interface KnowledgeEvidence {
  id: string;
  stance: string;
  note: string;
  created_at: string;
  source: KnowledgeSource;
}

export interface SearchKnowledgeNodesResponse {
  nodes: KnowledgeNode[];
  total: number;
  semantic: boolean;
}

export interface ListKnowledgeNodesResponse {
  nodes: KnowledgeNode[];
  total: number;
}

export interface GetKnowledgeNodeResponse {
  node: KnowledgeNode;
}

export interface ListKnowledgeEdgesResponse {
  edges: KnowledgeEdge[];
  total: number;
}

export interface GetKnowledgeEdgeResponse {
  edge: KnowledgeEdge;
  evidence: KnowledgeEvidence[];
}

export interface KnowledgeGraphResponse {
  focus: KnowledgeRef | null;
  nodes: KnowledgeNode[];
  refs: KnowledgeRef[];
  edges: KnowledgeEdge[];
  hops: number;
  truncated: boolean;
}

/** When found=false the server omits everything but found/truncated; the
 *  schema defaults the collections to empty. */
export interface KnowledgePathResponse {
  found: boolean;
  hops: number;
  edges: KnowledgeEdge[];
  nodes: KnowledgeNode[];
  refs: KnowledgeRef[];
  truncated: boolean;
}

export interface UpdateKnowledgeNodeRequest {
  title?: string;
  summary?: string;
  content?: string;
  aliases?: string[];
  attrs?: Record<string, unknown>;
  status?: string;
}

export interface SearchKnowledgeParams {
  q: string;
  limit?: number;
  signal?: AbortSignal;
}

export interface ListKnowledgeNodesParams {
  kind?: string;
  status?: string;
  limit?: number;
  offset?: number;
}

/** Endpoint mode lists edges touching one endpoint; status mode (no
 *  endpoint_id) lists live edges in a given status — the review queue. */
export interface ListKnowledgeEdgesParams {
  endpoint_id?: string;
  endpoint_type?: string;
  predicate?: string;
  include_closed?: boolean;
  status?: string;
  limit?: number;
}

export interface KnowledgeGraphParams {
  focus: string;
  focus_type?: string;
  hops?: number;
  include_proposed?: boolean;
  min_confidence?: number;
  limit?: number;
}

export interface KnowledgePathParams {
  src: string;
  dst: string;
  src_type?: string;
  dst_type?: string;
  max_hops?: number;
  include_proposed?: boolean;
}
