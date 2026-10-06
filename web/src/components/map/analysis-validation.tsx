import { cn } from "@/lib/utils"

/**
 * Validation status for each power-flow analysis study -- the single source of
 * truth surfaced as a badge on every analysis panel's "How this was calculated"
 * strip (see AnalysisExplanation). The point is honesty: a user must be able to
 * tell a number that has been checked against hand calculations or canonical
 * reference data from one that is plausible but unverified, so no study's output
 * is silently over-trusted.
 *
 *   validated    -- checked against independent hand calculations or canonical
 *                   reference data (e.g. IEEE test-feeder line impedances).
 *   derived      -- built directly on a validated core (the load-flow sweep or
 *                   fault engine) but the study's own layer is not independently
 *                   tested.
 *   experimental -- novel or complex and NOT yet verified against a reference;
 *                   treat the output as indicative, not authoritative.
 *
 * Keep this in sync with the validation-status table in the engineering-studies
 * documentation and the bet2-accuracy-validation work.
 */
export type ValidationTier = "validated" | "derived" | "experimental"

export interface ValidationInfo {
  tier: ValidationTier
  note: string
}

// Keyed by the analysis study's slug (matches the POST /analysis/<slug> route).
export const ANALYSIS_VALIDATION: Record<string, ValidationInfo> = {
  "load-flow": {
    tier: "validated",
    note: "Balanced forward-backward sweep. The drop/current kernels and the way the backward and forward passes compose are checked against hand calculations (including transformer rebasing).",
  },
  "fault-current": {
    tier: "validated",
    note: "3-phase and single-line-to-ground fault current checked against hand calculations on an explicit voltage basis; the embedded-generation infeed round-trips a machine's nameplate fault kA exactly.",
  },
  "unbalanced-load-flow": {
    tier: "validated",
    note: "The coupled (Carson) mutual coupling is validated against the IEEE 13-node feeder's Configuration 601 line impedances (off-diagonal terms match to 4 decimals). The decoupled Tier-1 path and the Carson self-impedance diagonal are not independently verified.",
  },
  "voltage-profile": {
    tier: "derived",
    note: "Reuses the validated load-flow sweep; the per-unit ANSI band check and the I²R loss summary are arithmetic over that solve, not independently tested.",
  },
  ampacity: {
    tier: "derived",
    note: "First-principles IEEE 738 steady-state thermal balance. Standards-based, but not cross-checked against a reference implementation here.",
  },
  coordination: {
    tier: "derived",
    note: "Uses the validated fault engine for the through-fault current at each device; the time-overcurrent curve evaluation and grading checks are not independently verified.",
  },
  contingency: {
    tier: "derived",
    note: "Reuses the validated load-flow sweep to re-solve a backfed section with a tie closed; the transfer/pick-up logic is not independently tested.",
  },
  "hosting-capacity": {
    tier: "derived",
    note: "Binary search over the validated load-flow sweep for the first binding over-voltage or overload; the search and constraint logic are not independently tested.",
  },
  "ev-hosting": {
    tier: "derived",
    note: "Binary search over the validated load-flow sweep (the under-voltage mirror of hosting capacity); not independently tested.",
  },
  "meshed-load-flow": {
    tier: "derived",
    note: "Solves a radial spanning tree with the validated sweep, then closes each loop by compensation; the loop compensation is not independently verified.",
  },
  "load-flow-auto": {
    tier: "derived",
    note: "A router that classifies topology and selects the radial, meshed, or nodal engine. It inherits the validation status of whichever engine actually runs.",
  },
  "nodal-load-flow": {
    tier: "experimental",
    note: "Full Y-bus Gauss-Seidel nodal solver for general (meshed) networks. Not yet verified against a reference power-flow.",
  },
  "arc-flash": {
    tier: "experimental",
    note: "IEEE 1584 incident energy, arc-flash boundary, and PPE category. Safety-relevant and NOT independently verified — treat as indicative only, never as the basis for PPE selection or energized-work permits.",
  },
  "volt-var": {
    tier: "experimental",
    note: "Volt-VAR control study. Not verified against a reference.",
  },
  "tap-optimization": {
    tier: "experimental",
    note: "Transformer/regulator tap-position optimization. Not verified against a reference.",
  },
  "storage-dispatch": {
    tier: "experimental",
    note: "Battery-storage dispatch study. Not verified against a reference.",
  },
  vvo: {
    tier: "experimental",
    note: "Combined Volt-VAR optimization. Not verified against a reference.",
  },
}

const TIER_STYLE: Record<ValidationTier, { label: string; className: string }> = {
  validated: {
    label: "Validated",
    className: "border-emerald-500/30 bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  },
  derived: {
    label: "Derived",
    className: "border-amber-500/30 bg-amber-500/15 text-amber-700 dark:text-amber-400",
  },
  experimental: {
    label: "Experimental",
    className: "border-red-500/30 bg-red-500/15 text-red-700 dark:text-red-400",
  },
}

/**
 * A small pill showing a study's validation tier. The full note is the native
 * tooltip; AnalysisExplanation also renders the note in the expanded strip.
 */
export function ValidationBadge({ study, className }: { study: string; className?: string }) {
  const info = ANALYSIS_VALIDATION[study]
  if (!info) return null
  const style = TIER_STYLE[info.tier]
  return (
    <span
      title={style.label + " — " + info.note}
      className={cn(
        "inline-flex items-center rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide",
        style.className,
        className,
      )}
    >
      {style.label}
    </span>
  )
}
