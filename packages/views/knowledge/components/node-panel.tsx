"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, X } from "lucide-react";
import {
  knowledgeEdgeOptions,
  knowledgeNodeOptions,
  useUpdateKnowledgeNodeStatus,
} from "@multica/core/knowledge";
import type { KnowledgeEdge } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Markdown } from "../../common/markdown";
import { useT } from "../../i18n";
import { KIND_COLORS, REF_COLOR } from "../lib/graph-model";

interface NodePanelProps {
  wsId: string;
  nodeId: string;
  /** Edges of the currently loaded graph touching this node. */
  edges: KnowledgeEdge[];
  /** Resolves a display label for any endpoint in the loaded graph. */
  labelFor: (type: string, id: string) => string;
  onSelectEndpoint: (id: string) => void;
  onClose: () => void;
}

/** One connection row: the other endpoint + expandable evidence list. */
function EdgeRow({
  wsId,
  edge,
  nodeId,
  labelFor,
  onSelectEndpoint,
}: {
  wsId: string;
  edge: KnowledgeEdge;
  nodeId: string;
  labelFor: (type: string, id: string) => string;
  onSelectEndpoint: (id: string) => void;
}) {
  const { t } = useT("knowledge");
  const [expanded, setExpanded] = useState(false);
  const detail = useQuery({ ...knowledgeEdgeOptions(wsId, edge.id), enabled: expanded });

  const otherType = edge.src_id === nodeId ? edge.dst_type : edge.src_type;
  const otherId = edge.src_id === nodeId ? edge.dst_id : edge.src_id;
  const evidence = detail.data?.evidence ?? [];

  return (
    <li className="rounded-md border">
      <div className="flex items-center gap-1 px-2 py-1.5">
        <button
          type="button"
          aria-label={t(($) => $.panel.evidence)}
          className="shrink-0 text-muted-foreground hover:text-foreground"
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        </button>
        <button
          type="button"
          className="min-w-0 flex-1 truncate text-left text-sm hover:underline"
          onClick={() => onSelectEndpoint(otherId)}
        >
          {labelFor(otherType, otherId)}
        </button>
        <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
          {Math.round(edge.confidence * 100)}%
        </span>
      </div>
      {expanded && (
        <ul className="space-y-1 border-t px-2 py-1.5">
          {evidence.length === 0 ? (
            <li className="text-xs text-muted-foreground">—</li>
          ) : (
            evidence.map((ev) => (
              <li key={ev.id} className="text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{ev.stance}</span>
                {ev.source.title ? <span> · {ev.source.title}</span> : null}
                {ev.note ? <span> — {ev.note}</span> : null}
              </li>
            ))
          )}
        </ul>
      )}
    </li>
  );
}

/**
 * Right-hand detail panel for the node selected on the canvas. Node data
 * comes from its own query (the graph response carries the full node, but
 * the query keeps the panel fresh after review actions).
 */
export function NodePanel({ wsId, nodeId, edges, labelFor, onSelectEndpoint, onClose }: NodePanelProps) {
  const { t } = useT("knowledge");
  const nodeQuery = useQuery(knowledgeNodeOptions(wsId, nodeId));
  const updateStatus = useUpdateKnowledgeNodeStatus(wsId);
  const node = nodeQuery.data;

  const byPredicate = useMemo(() => {
    const groups = new Map<string, KnowledgeEdge[]>();
    for (const edge of edges) {
      if (edge.src_id !== nodeId && edge.dst_id !== nodeId) continue;
      const list = groups.get(edge.predicate) ?? [];
      list.push(edge);
      groups.set(edge.predicate, list);
    }
    return groups;
  }, [edges, nodeId]);

  if (!node || node.id === "") return null;

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-start gap-2 border-b p-4">
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-sm font-semibold">{node.title}</h2>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline" className="gap-1.5">
              <span
                aria-hidden
                className="size-2 rounded-full"
                style={{ backgroundColor: KIND_COLORS[node.kind] ?? REF_COLOR }}
              />
              {node.kind}
            </Badge>
            <Badge variant={node.status === "proposed" ? "secondary" : "outline"}>
              {node.status}
            </Badge>
          </div>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label={t(($) => $.panel.close)} onClick={onClose}>
          <X className="size-4" />
        </Button>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
        {node.status === "proposed" && (
          <div className="flex gap-2">
            <Button
              size="sm"
              disabled={updateStatus.isPending}
              onClick={() => updateStatus.mutate({ id: node.id, status: "confirmed" })}
            >
              {t(($) => $.panel.approve)}
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={updateStatus.isPending}
              onClick={() => updateStatus.mutate({ id: node.id, status: "rejected" })}
            >
              {t(($) => $.panel.reject)}
            </Button>
          </div>
        )}

        {node.summary ? (
          <section>
            <h3 className="mb-1 text-xs font-medium text-muted-foreground">
              {t(($) => $.panel.summary)}
            </h3>
            <p className="text-sm">{node.summary}</p>
          </section>
        ) : null}

        {node.aliases.length > 0 && (
          <section>
            <h3 className="mb-1 text-xs font-medium text-muted-foreground">
              {t(($) => $.panel.aliases)}
            </h3>
            <div className="flex flex-wrap gap-1">
              {node.aliases.map((alias) => (
                <Badge key={alias} variant="secondary">
                  {alias}
                </Badge>
              ))}
            </div>
          </section>
        )}

        {node.content ? (
          <section>
            <Markdown>{node.content}</Markdown>
          </section>
        ) : null}

        <section>
          <h3 className="mb-1 text-xs font-medium text-muted-foreground">
            {t(($) => $.panel.connections)}
          </h3>
          {byPredicate.size === 0 ? (
            <p className="text-sm text-muted-foreground">{t(($) => $.panel.no_connections)}</p>
          ) : (
            <div className="space-y-3">
              {[...byPredicate.entries()].map(([predicate, group]) => (
                <div key={predicate}>
                  <p className="mb-1 text-xs text-muted-foreground">{predicate}</p>
                  <ul className="space-y-1">
                    {group.map((edge) => (
                      <EdgeRow
                        key={edge.id}
                        wsId={wsId}
                        edge={edge}
                        nodeId={nodeId}
                        labelFor={labelFor}
                        onSelectEndpoint={onSelectEndpoint}
                      />
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
