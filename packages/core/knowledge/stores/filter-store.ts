"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { createWorkspaceAwareStorage, registerForWorkspaceRehydration } from "../../platform/workspace-storage";
import { defaultStorage } from "../../platform/storage";

// Graph exploration filters. Hops and the proposed-material toggle shape
// every /graph request, so they persist per workspace like other view
// stores. Search text and the selected node stay session-local.
export interface KnowledgeViewState {
  /** Traversal depth for /graph requests (server clamps to 1..4). */
  hops: number;
  /** Whether proposed (unreviewed) nodes/edges are included. */
  includeProposed: boolean;
  /** Minimum edge confidence in [0, 1]; 0 = no filter. */
  minConfidence: number;
  setHops: (hops: number) => void;
  setIncludeProposed: (include: boolean) => void;
  setMinConfidence: (min: number) => void;
}

const DEFAULTS = {
  hops: 2,
  includeProposed: false,
  minConfidence: 0,
};

export const useKnowledgeViewStore = create<KnowledgeViewState>()(
  persist(
    (set) => ({
      ...DEFAULTS,
      setHops: (hops) => set({ hops }),
      setIncludeProposed: (include) => set({ includeProposed: include }),
      setMinConfidence: (min) => set({ minConfidence: min }),
    }),
    {
      name: "multica_knowledge_view",
      storage: createJSONStorage(() => createWorkspaceAwareStorage(defaultStorage)),
      partialize: (state) => ({
        hops: state.hops,
        includeProposed: state.includeProposed,
        minConfidence: state.minConfidence,
      }),
      // Merge on top of defaults so a payload persisted before a filter
      // existed still gets that key's default value.
      merge: (persisted, current) => {
        if (!persisted) return { ...current, ...DEFAULTS };
        return { ...current, ...(persisted as Partial<KnowledgeViewState>) };
      },
    }
  )
);

registerForWorkspaceRehydration(() => useKnowledgeViewStore.persist.rehydrate());
