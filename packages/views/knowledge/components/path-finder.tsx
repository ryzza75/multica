"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, MoveRight } from "lucide-react";
import { knowledgePathOptions, useKnowledgeViewStore } from "@multica/core/knowledge";
import type { KnowledgeNode, KnowledgePathParams } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";
import { NodeSearchInput } from "./node-search-input";
import { KIND_COLORS, REF_COLOR } from "../lib/graph-model";

interface PathFinderProps {
  wsId: string;
}

/**
 * Find the shortest chain between two knowledge nodes and render it as an
 * ordered node → predicate → node sequence.
 */
export function PathFinder({ wsId }: PathFinderProps) {
  const { t } = useT("knowledge");
  const includeProposed = useKnowledgeViewStore((s) => s.includeProposed);
  const [src, setSrc] = useState<KnowledgeNode | null>(null);
  const [dst, setDst] = useState<KnowledgeNode | null>(null);
  const [submitted, setSubmitted] = useState<KnowledgePathParams | null>(null);

  const pathQuery = useQuery({
    ...knowledgePathOptions(wsId, submitted ?? { src: "", dst: "" }),
    enabled: submitted !== null,
  });
  const path = pathQuery.data;

  // Walk edges src → dst so each step renders "predicate → next endpoint".
  const chain = useMemo(() => {
    if (!path?.found || !submitted) return null;
    const labelById = new Map<string, { label: string; kind: string; isRef: boolean }>();
    for (const n of path.nodes) labelById.set(n.id, { label: n.title, kind: n.kind, isRef: false });
    for (const r of path.refs) {
      if (!labelById.has(r.id)) {
        labelById.set(r.id, { label: `${r.type} ${r.id.slice(0, 8)}`, kind: r.type, isRef: true });
      }
    }
    const steps: { edgeId: string; predicate: string; endpointId: string }[] = [];
    let current = submitted.src;
    for (const edge of path.edges) {
      const next = edge.src_id === current ? edge.dst_id : edge.src_id;
      steps.push({ edgeId: edge.id, predicate: edge.predicate, endpointId: next });
      current = next;
    }
    return { start: submitted.src, steps, labelById };
  }, [path, submitted]);

  const endpointBadge = (id: string, labelById: Map<string, { label: string; kind: string; isRef: boolean }>) => {
    const entry = labelById.get(id);
    return (
      <Badge variant="outline" className="gap-1.5">
        <span
          aria-hidden
          className="size-2 rounded-full"
          style={{ backgroundColor: entry && !entry.isRef ? KIND_COLORS[entry.kind] ?? REF_COLOR : REF_COLOR }}
        />
        {entry?.label ?? id.slice(0, 8)}
      </Badge>
    );
  };

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="min-w-0 flex-1">
          <p className="mb-1 text-xs font-medium text-muted-foreground">
            {t(($) => $.path.source)}
          </p>
          <NodeSearchInput wsId={wsId} onPick={setSrc} />
        </div>
        <div className="min-w-0 flex-1">
          <p className="mb-1 text-xs font-medium text-muted-foreground">
            {t(($) => $.path.target)}
          </p>
          <NodeSearchInput wsId={wsId} onPick={setDst} />
        </div>
        <Button
          disabled={!src || !dst || src.id === dst.id}
          onClick={() => {
            if (!src || !dst) return;
            setSubmitted({
              src: src.id,
              dst: dst.id,
              src_type: "node",
              dst_type: "node",
              include_proposed: includeProposed,
            });
          }}
        >
          {t(($) => $.path.find)}
        </Button>
      </div>

      {path && !path.found && (
        <div className="rounded-md border p-4 text-sm text-muted-foreground">
          <p>{t(($) => $.path.not_found)}</p>
          {path.truncated === true && (
            <p className="mt-1 text-xs">{t(($) => $.path.truncated)}</p>
          )}
        </div>
      )}

      {chain && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border p-4">
          {endpointBadge(chain.start, chain.labelById)}
          {chain.steps.map((step) => (
            <span key={step.edgeId} className="flex items-center gap-2">
              <span className="flex items-center gap-1 text-xs text-muted-foreground">
                <MoveRight className="size-3.5" aria-hidden />
                {step.predicate}
                <ArrowRight className="size-3.5" aria-hidden />
              </span>
              {endpointBadge(step.endpointId, chain.labelById)}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
