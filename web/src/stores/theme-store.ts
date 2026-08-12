import { create } from "zustand"
import { persist } from "zustand/middleware"

export type Theme = "light" | "dark" | "system"

interface ThemeState {
  theme: Theme
  setTheme: (theme: Theme) => void
}

/**
 * Just the user's preference ("system" included) -- resolving "system"
 * against the OS media query and applying the .dark class to <html> are
 * DOM side effects, deliberately kept out of the store and done in
 * useThemeEffect (src/hooks/use-theme-effect.ts) instead. Persisted so
 * an explicit light/dark choice survives a reload; "system" is also the
 * default for a first-ever visit.
 */
export const useThemeStore = create<ThemeState>()(
  persist(
    (set) => ({
      theme: "system",
      setTheme: (theme) => set({ theme }),
    }),
    { name: "voltedge-theme" },
  ),
)
