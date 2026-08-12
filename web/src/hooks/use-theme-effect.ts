import { useEffect } from "react"

import { useThemeStore } from "@/stores/theme-store"

/**
 * Resolves the store's theme preference ("light" | "dark" | "system")
 * against the OS-level media query and applies/removes the .dark class
 * on <html> -- the one DOM side effect the theme system needs. Mount
 * once at the app root (see src/main.tsx). Listens for OS theme changes
 * live so a system-theme user who flips their OS setting sees the app
 * follow without a reload.
 */
export function useThemeEffect() {
  const theme = useThemeStore((s) => s.theme)

  useEffect(() => {
    const root = document.documentElement
    const media = window.matchMedia("(prefers-color-scheme: dark)")

    const apply = () => {
      const resolved = theme === "system" ? (media.matches ? "dark" : "light") : theme
      root.classList.toggle("dark", resolved === "dark")
      root.style.colorScheme = resolved
    }

    apply()

    if (theme !== "system") return
    media.addEventListener("change", apply)
    return () => media.removeEventListener("change", apply)
  }, [theme])
}
