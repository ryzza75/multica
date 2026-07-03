"use client";

import { useEffect, useRef } from "react";
import Graph from "graphology";
import { circular } from "graphology-layout";
import forceAtlas2 from "graphology-layout-forceatlas2";
import Sigma from "sigma";
import { Waypoints } from "lucide-react";
import { useT } from "../../i18n";
import type { GraphModel } from "../lib/graph-model";

// Alpha suffixes for #rrggbbaa colors (sigma parses 8-digit hex). Proposed
// material and de-emphasized (non-neighbor) elements render translucent
// rather than swapping palette entries.
const PROPOSED_ALPHA = "80";
const DIMMED_ALPHA = "22";
const EDGE_BASE_COLOR = "#8d8d86";

interface GraphCanvasProps {
  model: GraphModel;
  selectedId: string | null;
  onSelectNode: (key: string) => void;
  /** Double-click: load the node's neighborhood into the canvas. */
  onExpandNode: (key: string) => void;
}

/**
 * Sigma v3 canvas. SSR-safe: the Sigma instance is only created inside an
 * effect against a container ref. The instance is rebuilt when the model
 * changes (layout depends on the full topology) and killed on unmount;
 * selection highlighting only swaps reducers on the live instance.
 */
export function GraphCanvas({ model, selectedId, onSelectNode, onExpandNode }: GraphCanvasProps) {
  const { t } = useT("knowledge");
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  // Callbacks go through refs so a re-created parent closure doesn't force
  // a full sigma rebuild.
  const onSelectRef = useRef(onSelectNode);
  onSelectRef.current = onSelectNode;
  const onExpandRef = useRef(onExpandNode);
  onExpandRef.current = onExpandNode;

  const isEmpty = model.nodes.length === 0;

  useEffect(() => {
    const container = containerRef.current;
    if (!container || isEmpty) return;

    const graph = new Graph({ multi: true });
    for (const node of model.nodes) {
      graph.addNode(node.key, {
        label: node.label,
        size: node.size,
        color: node.faded ? `${node.color}${PROPOSED_ALPHA}` : node.color,
        baseColor: node.color,
        faded: node.faded,
      });
    }
    for (const edge of model.edges) {
      if (graph.hasNode(edge.source) && graph.hasNode(edge.target)) {
        graph.addEdgeWithKey(edge.key, edge.source, edge.target, {
          label: edge.predicate,
          size: edge.dashed ? 1 : Math.max(1, edge.confidence * 3),
          // Sigma's default edge programs can't draw dashes, so proposed
          // edges get the lowered-opacity treatment instead.
          color: edge.dashed ? `${EDGE_BASE_COLOR}${PROPOSED_ALPHA}` : EDGE_BASE_COLOR,
          dashed: edge.dashed,
        });
      }
    }

    circular.assign(graph);
    forceAtlas2.assign(graph, {
      iterations: 200,
      settings: forceAtlas2.inferSettings(graph),
    });

    // Match label color to the surrounding theme without hardcoding a
    // light/dark pair: the container inherits the semantic foreground.
    const labelColor = getComputedStyle(container).color || "#71717a";

    const sigma = new Sigma(graph, container, {
      renderEdgeLabels: true,
      labelColor: { color: labelColor },
      edgeLabelColor: { color: labelColor },
      labelSize: 11,
      edgeLabelSize: 9,
      labelRenderedSizeThreshold: 7,
      doubleClickZoomingRatio: 1,
    });
    sigmaRef.current = sigma;

    sigma.on("clickNode", ({ node }) => {
      onSelectRef.current(node);
    });
    sigma.on("doubleClickNode", (event) => {
      event.preventSigmaDefault();
      onExpandRef.current(event.node);
    });
    sigma.on("clickStage", () => {
      onSelectRef.current("");
    });

    return () => {
      sigmaRef.current = null;
      sigma.kill();
    };
  }, [model, isEmpty]);

  // Selection highlight: keep the selected node and its neighbors at full
  // strength, fade everything else. Reducers re-run on refresh.
  useEffect(() => {
    const sigma = sigmaRef.current;
    if (!sigma) return;
    const graph = sigma.getGraph();
    const hasSelection = selectedId !== null && selectedId !== "" && graph.hasNode(selectedId);
    const neighborhood = new Set<string>();
    if (hasSelection) {
      neighborhood.add(selectedId);
      for (const neighbor of graph.neighbors(selectedId)) neighborhood.add(neighbor);
    }

    sigma.setSetting("nodeReducer", (node, data) => {
      if (!hasSelection) return data;
      const base = (data.baseColor as string) ?? (data.color as string);
      if (node === selectedId) {
        return { ...data, color: base, highlighted: true, zIndex: 2 };
      }
      if (neighborhood.has(node)) {
        return { ...data, color: base, zIndex: 1 };
      }
      return { ...data, color: `${base}${DIMMED_ALPHA}`, label: "", zIndex: 0 };
    });
    sigma.setSetting("edgeReducer", (edge, data) => {
      if (!hasSelection) return data;
      const touchesSelection =
        graph.source(edge) === selectedId || graph.target(edge) === selectedId;
      if (touchesSelection) return { ...data, zIndex: 1 };
      return { ...data, color: `${EDGE_BASE_COLOR}${DIMMED_ALPHA}`, label: "", zIndex: 0 };
    });
    sigma.refresh();
  }, [selectedId, model]);

  if (isEmpty) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
        <Waypoints className="size-8" aria-hidden />
        <p className="text-sm">{t(($) => $.explore.empty)}</p>
      </div>
    );
  }

  return <div ref={containerRef} className="h-full w-full text-muted-foreground" />;
}
