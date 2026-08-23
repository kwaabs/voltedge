import { create } from "zustand"

/**
 * Not persisted -- now purely an instant-feedback optimization, no longer
 * load-bearing for correctness. The real fix landed server-side (migration
 * 0038 + infra/martin/config.yaml): Martin publishes per-class tile
 * *functions* that filter deleted_at IS NULL instead of the raw tables, so a
 * soft-deleted feature is genuinely gone from every tile the server hands out,
 * across reloads and for every user. But OL's renderer keeps already-fetched
 * tiles cached by URL, so a feature just deleted this session would linger in
 * the map until those tiles are refetched (a pan into fresh tiles, a
 * refresh-version bump, or a reload). To hide it the instant the delete
 * succeeds -- using tile data already in the browser's cache, no refetch --
 * its id is remembered here and map-layers.ts's styleForFeature paints it with
 * an empty style. A reload clears this set, and that's fine now: the server's
 * filtered tiles already exclude the feature.
 */
interface DeletedFeaturesState {
  ids: Set<string>
  markDeleted: (featureId: string) => void
}

export const useDeletedFeaturesStore = create<DeletedFeaturesState>((set) => ({
  ids: new Set(),
  markDeleted: (featureId) => set((state) => ({ ids: new Set(state.ids).add(featureId) })),
}))
