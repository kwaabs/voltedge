import { createJSONStorage, type PersistOptions } from "zustand/middleware"

/**
 * Shared `persist` options for the analysis/trace RESULT stores (trace,
 * isolate, and every powerflow analysis). Backed by sessionStorage, so a
 * completed result survives a full page reload and route navigation but clears
 * when the tab closes -- these results are point-in-time snapshots that can go
 * stale against later feature edits, so a per-tab lifetime fits better than the
 * permanent localStorage the durable preference stores (units/layers/theme) use.
 *
 * `partialize` persists every data field EXCEPT:
 *  - `loading` -- a transient in-flight flag; restoring it would rehydrate a
 *    stuck spinner for a request that is no longer running.
 *  - any function -- actions (setResult/clear/arm/...) are dropped and come back
 *    from each store's own initializer on rehydrate, and a non-serialisable
 *    thunk like trace-store's `rerun` is dropped too (its Re-run button simply
 *    hides until the next run re-stashes it).
 * Doing it generically (filter by key/type) rather than naming fields per store
 * keeps one helper correct across all their differing result shapes.
 */
export function resultPersist<T>(name: string): PersistOptions<T, Partial<T>> {
  return {
    name,
    storage: createJSONStorage(() => sessionStorage),
    partialize: (state) =>
      Object.fromEntries(
        Object.entries(state as Record<string, unknown>).filter(
          ([key, value]) => typeof value !== "function" && key !== "loading",
        ),
      ) as Partial<T>,
  }
}
