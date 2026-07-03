"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Check, X } from "lucide-react";
import {
  knowledgeNodeOptions,
  knowledgeReviewEdgesOptions,
  knowledgeReviewNodesOptions,
  useUpdateKnowledgeEdgeStatus,
  useUpdateKnowledgeNodeStatus,
} from "@multica/core/knowledge";
import type { KnowledgeEdge, KnowledgeNode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n";

interface KnowledgeReviewProps {
  wsId: string;
  /** Focus the explore view on a node (e.g. to inspect before approving). */
  onInspectNode: (idOrSlug: string) => void;
}

/** Resolves a display label for an edge endpoint. Node endpoints fetch
 *  their title (React Query dedupes repeats); other entity types render a
 *  generic type chip — the UI resolves those through their own pages. */
function EndpointLabel({ wsId, type, id }: { wsId: string; type: string; id: string }) {
  const isNode = type === "node";
  const { data: node } = useQuery({
    ...knowledgeNodeOptions(wsId, id),
    enabled: isNode,
  });
  if (!isNode) {
    return (
      <Badge variant="outline" className="font-mono text-[10px]">
        {type}
      </Badge>
    );
  }
  return <span className="truncate font-medium">{node?.title ?? "…"}</span>;
}

function ReviewActions({
  onApprove,
  onReject,
  approveLabel,
  rejectLabel,
}: {
  onApprove: () => void;
  onReject: () => void;
  approveLabel: string;
  rejectLabel: string;
}) {
  return (
    <div className="flex shrink-0 items-center gap-1.5">
      <Button size="sm" variant="outline" onClick={onApprove}>
        <Check className="size-3.5" aria-hidden />
        {approveLabel}
      </Button>
      <Button size="sm" variant="ghost" className="text-muted-foreground" onClick={onReject}>
        <X className="size-3.5" aria-hidden />
        {rejectLabel}
      </Button>
    </div>
  );
}

/**
 * Curation queue: everything agents proposed, waiting for a member to
 * confirm or reject. Approve/reject removes the row optimistically (the
 * mutation hooks own rollback + re-sync).
 */
export function KnowledgeReview({ wsId, onInspectNode }: KnowledgeReviewProps) {
  const { t } = useT("knowledge");
  const nodesQuery = useQuery(knowledgeReviewNodesOptions(wsId));
  const edgesQuery = useQuery(knowledgeReviewEdgesOptions(wsId));
  const nodeStatus = useUpdateKnowledgeNodeStatus(wsId);
  const edgeStatus = useUpdateKnowledgeEdgeStatus(wsId);

  if (nodesQuery.isLoading || edgesQuery.isLoading) {
    return (
      <div className="flex flex-col gap-2 p-4">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-2/3" />
      </div>
    );
  }

  const nodes: KnowledgeNode[] = nodesQuery.data?.nodes ?? [];
  const edges: KnowledgeEdge[] = edgesQuery.data?.edges ?? [];

  if (nodes.length === 0 && edges.length === 0) {
    return (
      <div className="flex h-full items-center justify-center p-8 text-sm text-muted-foreground">
        {t(($) => $.review.empty)}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6 overflow-y-auto p-4">
      {nodes.length > 0 && (
        <section className="flex flex-col gap-2">
          <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {t(($) => $.review.proposed_nodes)}
          </h3>
          <ul className="flex flex-col divide-y divide-border rounded-md border">
            {nodes.map((node) => (
              <li key={node.id} className="flex items-center gap-3 px-3 py-2">
                <button
                  type="button"
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                  onClick={() => onInspectNode(node.id)}
                >
                  <Badge variant="outline" className="shrink-0 text-[10px]">
                    {node.kind}
                  </Badge>
                  <span className="truncate text-sm font-medium">{node.title}</span>
                  {node.summary && (
                    <span className="hidden truncate text-xs text-muted-foreground sm:inline">
                      {node.summary}
                    </span>
                  )}
                </button>
                <ReviewActions
                  approveLabel={t(($) => $.review.approve)}
                  rejectLabel={t(($) => $.review.reject)}
                  onApprove={() => nodeStatus.mutate({ id: node.id, status: "confirmed" })}
                  onReject={() => nodeStatus.mutate({ id: node.id, status: "rejected" })}
                />
              </li>
            ))}
          </ul>
        </section>
      )}
      {edges.length > 0 && (
        <section className="flex flex-col gap-2">
          <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {t(($) => $.review.proposed_edges)}
          </h3>
          <ul className="flex flex-col divide-y divide-border rounded-md border">
            {edges.map((edge) => (
              <li key={edge.id} className="flex items-center gap-3 px-3 py-2">
                <div className="flex min-w-0 flex-1 items-center gap-2 text-sm">
                  <EndpointLabel wsId={wsId} type={edge.src_type} id={edge.src_id} />
                  <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                    <ArrowRight className="size-3.5" aria-hidden />
                    {edge.predicate}
                    <ArrowRight className="size-3.5" aria-hidden />
                  </span>
                  <EndpointLabel wsId={wsId} type={edge.dst_type} id={edge.dst_id} />
                </div>
                <ReviewActions
                  approveLabel={t(($) => $.review.approve)}
                  rejectLabel={t(($) => $.review.reject)}
                  onApprove={() => edgeStatus.mutate({ id: edge.id, status: "confirmed" })}
                  onReject={() => edgeStatus.mutate({ id: edge.id, status: "rejected" })}
                />
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
