import { useState } from "react"
import type { ReactNode } from "react"
import { ChevronRight } from "lucide-react"

import { cn } from "@/lib/utils"
import { ANALYSIS_VALIDATION, ValidationBadge } from "@/components/map/analysis-validation"

/**
 * A collapsible "how this was calculated" strip pinned to the bottom of every
 * floating analysis panel (fault-current, load-flow, coordination, voltage-
 * profile, contingency, hosting, ampacity, arc-flash, and the meshed/nodal/auto
 * load-flow variants). It sits OUTSIDE the panel's scrollable result region so
 * it stays reachable no matter how long the result table is, and it is
 * **collapsed by default** -- an engineer expands it to check the working.
 *
 * The `children` an analysis passes are deliberately NOT static boilerplate:
 * each panel threads the run's own computed figures (the governing node, the
 * key value, counts, the standard/formula actually applied, and any honesty
 * flags) into the prose, so the explanation describes *this* answer and can be
 * used to verify it. See fault-current-panel.tsx for the canonical shape.
 *
 * `title` overrides the trigger label (default "How this was calculated") for
 * the rare panel where "calculated" reads oddly (e.g. a topological study).
 *
 * `study` is the analysis slug (e.g. "fault-current"); when given, a validation-
 * status badge is pinned to the always-visible header and the study's honesty
 * note is shown first in the expanded body, so an unverified result is never
 * silently over-trusted. See analysis-validation.tsx.
 */
export function AnalysisExplanation({
  children,
  title = "How this was calculated",
  study,
}: {
  children: ReactNode
  title?: string
  study?: string
}) {
  const [open, setOpen] = useState(false)
  const info = study ? ANALYSIS_VALIDATION[study] : undefined
  return (
    <div className="border-t border-primary/15 bg-muted/30 shrink-0">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="text-muted-foreground hover:text-foreground flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-xs font-medium"
      >
        <ChevronRight className={cn("size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
        <span>{title}</span>
        {study && <ValidationBadge study={study} className="ml-auto" />}
      </button>
      {open && (
        <div className="text-muted-foreground max-h-56 space-y-1.5 overflow-y-auto px-3 pt-0.5 pb-3 text-xs leading-relaxed [&_b]:text-foreground [&_b]:font-medium [&_code]:text-foreground [&_code]:font-mono [&_code]:text-[11px]">
          {info && (
            <p className="border-primary/20 bg-background/50 rounded border px-2 py-1.5">
              <b>Validation — {info.tier}.</b> {info.note}
            </p>
          )}
          {children}
        </div>
      )}
    </div>
  )
}

/**
 * A worked-calculation block for use inside AnalysisExplanation: the governing
 * equation with THIS run's numbers substituted so the result falls out and can
 * be checked by hand. Render the symbol form, the substituted form, and the
 * result as separate lines (newlines are preserved) -- e.g.
 *
 *   <Formula>{`Ibf = V_LN / |Z|
 *       = 133 V / 0.500 Ω
 *       = 266 A`}</Formula>
 *
 * It is monospaced and boxed so the arithmetic reads apart from the prose.
 */
export function Formula({ children }: { children: ReactNode }) {
  return (
    <pre className="text-foreground border-primary/20 bg-background/60 my-1.5 overflow-x-auto rounded border px-2.5 py-1.5 font-mono text-[11px] leading-relaxed whitespace-pre-wrap">
      {children}
    </pre>
  )
}
