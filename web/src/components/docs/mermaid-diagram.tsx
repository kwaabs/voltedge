import mermaid from "mermaid"
import { useEffect, useId, useState } from "react"

/**
 * Renders one fenced ```mermaid block from a manual into an SVG diagram. The
 * docs viewer's ReactMarkdown code override (routes/docs.tsx) mounts this in
 * place of the raw code element when the fence language is `mermaid`.
 *
 * mermaid.render is async and produces an SVG string we inject; a parse error
 * falls back to the raw source in a <pre> so a bad diagram never blanks the
 * page. Theme follows the app's class-based dark mode -- read once per render,
 * which is enough since the viewer remounts on navigation.
 */

let initialized = false

function ensureInitialized() {
  if (initialized) return
  const dark = document.documentElement.classList.contains("dark")
  mermaid.initialize({
    startOnLoad: false,
    theme: dark ? "dark" : "default",
    securityLevel: "strict",
    fontFamily: "inherit",
  })
  initialized = true
}

export function MermaidDiagram({ chart }: { chart: string }) {
  // useId gives a stable id across renders; strip the colons mermaid's own
  // selector lookups can't handle.
  const id = "mermaid-" + useId().replace(/:/g, "")
  const [svg, setSvg] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let cancelled = false
    ensureInitialized()
    mermaid
      .render(id, chart)
      .then(({ svg }) => {
        if (!cancelled) {
          setSvg(svg)
          setFailed(false)
        }
      })
      .catch(() => {
        if (!cancelled) setFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [id, chart])

  if (failed) {
    return (
      <pre className="my-5 overflow-x-auto rounded-md bg-muted p-4 text-sm">
        <code>{chart}</code>
      </pre>
    )
  }

  const wrapper = "my-6 flex justify-center overflow-x-auto rounded-md border bg-card p-4 [&_svg]:h-auto [&_svg]:max-w-full"

  if (!svg) {
    return (
      <div className={wrapper}>
        <span className="text-muted-foreground text-sm">Rendering diagram…</span>
      </div>
    )
  }

  return (
    <div
      className={wrapper}
      // mermaid output, sanitized by securityLevel:'strict'
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
