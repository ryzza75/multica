"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { knowledgeGraphOptions, useKnowledgeViewStore } from "@multica/core/knowledge";
import { useWorkspaceId } from "@multica/core/hooks";
import type { KnowledgeGraphResponse } from "@multica/core/types";
import { Switch } from "@multica/ui/components/ui/switch";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";
import { buildGraphModel, mergeGraphResponses } from "../lib/graph-model";
import { GraphCanvas } from "./graph-canvas";
import { KnowledgeReview } from "./knowledge-review";
import { NodePanel } from "./node-panel";
import { NodeSearchInput } from "./node-search-input";
import { PathFinder } from "./path-finder";

type KnowledgeTab = "explore" | "path" | "review";

const HOP_CHOICES = [1, 2, 3, 4] as const;

/**
 * The knowledge graph page: search-first exploration on a sigma canvas,
 * multi-hop path finding, and the proposed-material review queue.
 * Expansion accumulates neighborhoods client-side (mergeGraphResponses);
 * changing focus or filters resets to a fresh base graph.
 */
export function KnowledgePage() {
  const wsId = useWorkspaceId();
  const { t } = useT("knowledge");
  const qc = useQueryClient();

  const [tab, setTab] = useState<KnowledgeTab>("explore");
  const [focus, setFocus] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [graph, setGraph] = useState<KnowledgeGraphResponse | null>(null);

  const hops = useKnowledgeViewStore((s) => s.hops);
  const includeProposed = useKnowledgeViewStore((s) => s.includeProposed);
  const setHops = useKnowledgeViewStore((s) => s.setHops);
  const setIncludeProposed = useKnowledgeViewStore((s) => s.setIncludeProposed);

  const baseParams = useMemo(
    () => ({ focus: focus ?? "", hops, include_proposed: includeProposed }),
    [focus, hops, includeProposed],
  );
  const graphQuery = useQuery(knowledgeGraphOptions(wsId, baseParams));

  // A fresh base response replaces the accumulated graph: focus or filter
  // changes invalidate everything previously expanded on screen.
  useEffect(() => {
    if (graphQuery.data) {
      setGraph(graphQuery.data);
    }
  }, [graphQuery.data]);

  const expandNode = useCallback(
    async (key: string) => {
      const response = await qc.fetchQuery(
        knowledgeGraphOptions(wsId, {
          focus: key,
          hops: 1,
          include_proposed: includeProposed,
        }),
      );
      setGraph((prev) => (prev ? mergeGraphResponses(prev, response) : response));
    },
    [qc, wsId, includeProposed],
  );

  const model = useMemo(
    () => buildGraphModel(graph?.nodes ?? [], graph?.refs ?? [], graph?.edges ?? []),
    [graph],
  );

  const labelFor = useCallback(
    (type: string, id: string) => {
      if (type === "node") {
        const node = graph?.nodes.find((n) => n.id === id);
        if (node) return node.title;
      }
      return type;
    },
    [graph],
  );

  const selectedIsNode = selectedId !== null && (graph?.nodes.some((n) => n.id === selectedId) ?? false);
  const selectedEdges = useMemo(() => {
    if (!selectedId || !graph) return [];
    return graph.edges.filter((e) => e.src_id === selectedId || e.dst_id === selectedId);
  }, [graph, selectedId]);

  const focusFromReview = useCallback((idOrSlug: string) => {
    setTab("explore");
    setFocus(idOrSlug);
    setSelectedId(null);
  }, []);

  return (
    <div className="flex h-full flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
        <h1 className="text-lg font-semibold">{t(($) => $.title)}</h1>
        <Tabs value={tab} onValueChange={(value) => setTab(value as KnowledgeTab)}>
          <TabsList>
            <TabsTrigger value="explore">{t(($) => $.tabs.explore)}</TabsTrigger>
            <TabsTrigger value="path">{t(($) => $.tabs.path)}</TabsTrigger>
            <TabsTrigger value="review">{t(($) => $.tabs.review)}</TabsTrigger>
          </TabsList>
        </Tabs>
        {tab === "explore" && (
          <div className="flex flex-1 flex-wrap items-center justify-end gap-3">
            <div className="w-full max-w-xs">
              <NodeSearchInput
                wsId={wsId}
                onPick={(node) => {
                  setFocus(node.id);
                  setSelectedId(node.id);
                }}
              />
            </div>
            <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
              {t(($) => $.filters.hops)}
              <Select value={String(hops)} onValueChange={(value) => setHops(Number(value))}>
                <SelectTrigger size="sm" className="w-14">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {HOP_CHOICES.map((choice) => (
                    <SelectItem key={choice} value={String(choice)}>
                      {choice}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </label>
            <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
              {t(($) => $.filters.include_proposed)}
              <Switch checked={includeProposed} onCheckedChange={setIncludeProposed} />
            </label>
          </div>
        )}
      </header>

      {tab === "explore" && (
        <div className="flex min-h-0 flex-1">
          <div className="relative min-w-0 flex-1">
            {focus === null ? (
              <div className="flex h-full items-center justify-center p-8 text-sm text-muted-foreground">
                {t(($) => $.explore.empty)}
              </div>
            ) : (
              <>
                <GraphCanvas
                  model={model}
                  selectedId={selectedId}
                  onSelectNode={setSelectedId}
                  onExpandNode={(key) => void expandNode(key)}
                />
                {graph?.truncated === true && (
                  <div className="absolute bottom-3 left-3 flex items-center gap-1.5 rounded-md border bg-background/95 px-2.5 py-1.5 text-xs text-muted-foreground shadow-sm">
                    <TriangleAlert className="size-3.5" aria-hidden />
                    {t(($) => $.explore.truncated)}
                  </div>
                )}
              </>
            )}
          </div>
          {selectedIsNode && selectedId && (
            <aside className="w-96 shrink-0 overflow-y-auto border-l">
              <NodePanel
                wsId={wsId}
                nodeId={selectedId}
                edges={selectedEdges}
                labelFor={labelFor}
                onSelectEndpoint={setSelectedId}
                onClose={() => setSelectedId(null)}
              />
            </aside>
          )}
        </div>
      )}

      {tab === "path" && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <PathFinder wsId={wsId} />
        </div>
      )}

      {tab === "review" && (
        <div className="min-h-0 flex-1">
          <KnowledgeReview wsId={wsId} onInspectNode={focusFromReview} />
        </div>
      )}
    </div>
  );
}
