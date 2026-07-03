"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { knowledgeSearchOptions } from "@multica/core/knowledge";
import type { KnowledgeNode } from "@multica/core/types";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../i18n";
import { KIND_COLORS, REF_COLOR } from "../lib/graph-model";

const EMPTY_NODES: KnowledgeNode[] = [];

interface NodeSearchInputProps {
  wsId: string;
  onPick: (node: KnowledgeNode) => void;
  /** Explore mode clears the box after picking; path pickers keep the title. */
  clearOnPick?: boolean;
  placeholder?: string;
}

/**
 * Debounced knowledge search box with a results dropdown. Shared by the
 * explore tab (focus picker) and the path finder (src/dst pickers).
 */
export function NodeSearchInput({ wsId, onPick, clearOnPick = false, placeholder }: NodeSearchInputProps) {
  const { t } = useT("knowledge");
  const [text, setText] = useState("");
  const [debounced, setDebounced] = useState("");
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const handle = setTimeout(() => setDebounced(text.trim()), 250);
    return () => clearTimeout(handle);
  }, [text]);

  const search = useQuery(knowledgeSearchOptions(wsId, debounced, 10));
  const nodes = search.data?.nodes ?? EMPTY_NODES;
  const showDropdown = open && debounced.length > 0;

  return (
    <div className="relative">
      <Input
        value={text}
        placeholder={placeholder ?? t(($) => $.search.placeholder)}
        onChange={(event) => {
          setText(event.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
      />
      {showDropdown && (
        <div className="absolute z-20 mt-1 max-h-64 w-full overflow-y-auto rounded-md border bg-popover p-1 shadow-md">
          {nodes.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-muted-foreground">
              {t(($) => $.search.no_results)}
            </p>
          ) : (
            nodes.map((node) => (
              <button
                key={node.id}
                type="button"
                className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                // Keep focus in the input so onBlur doesn't close the
                // dropdown before the click lands.
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  onPick(node);
                  setText(clearOnPick ? "" : node.title);
                  setOpen(false);
                }}
              >
                <span
                  aria-hidden
                  className="size-2 shrink-0 rounded-full"
                  style={{ backgroundColor: KIND_COLORS[node.kind] ?? REF_COLOR }}
                />
                <span className="min-w-0 flex-1 truncate">{node.title}</span>
                <span className="shrink-0 text-xs text-muted-foreground">{node.kind}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}
