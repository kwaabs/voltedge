import { create } from "zustand"

/**
 * Not persisted -- a client-side symptom fix, not a real data-layer
 * concept. Martin auto-publishes each feature class's raw table
 * directly (confirmed by soft-deleting a real feature and fetching its
 * tile -- the feature was still there), so a soft-deleted feature stays
 * fully visible in the vector tiles it serves. Rather than the real
 * fix (Martin serving a deleted_at-filtered view instead of the raw
 * table -- a compiler/schema change out of scope for a
 * confirmation-dialog slice, see docs/phase-3-delete-confirmation.md's
 * "still open"), a successfully deleted feature's id is remembered here
 * so map-layers.ts's styleForFeature can hide it immediately, using
 * tile data already sitting in the browser's own cache -- no refetch.
 */
interface DeletedFeaturesState {
  ids: Set<string>
  markDeleted: (featureId: string) => void
}

export const useDeletedFeaturesStore = create<DeletedFeaturesState>((set) => ({
  ids: new Set(),
  markDeleted: (featureId) => set((state) => ({ ids: new Set(state.ids).add(featureId) })),
}))
