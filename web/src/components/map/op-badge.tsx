import { cn } from "@/lib/utils"

// A shared chip for a device operation's type (trip / lockout / reclose / open /
// close / earth / alarm), coloured by kind. Used by the Operations page and the
// device detail panel so both read identically. The terms read backwards to
// anyone not steeped in switching (open = contacts apart = circuit broken =
// de-energized), so the meaning is carried by a one-time OpLegend above the table
// rather than a tooltip on every row.

const OP_BADGE: Record<string, string> = {
  trip: "bg-destructive/10 text-destructive",
  lockout: "bg-destructive/15 text-destructive",
  reclose: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  open: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  close: "bg-green-600/10 text-green-700 dark:text-green-400",
  earth: "bg-muted text-muted-foreground",
  alarm: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  other: "bg-muted text-muted-foreground",
}

/** Terse gloss per op type for the legend. */
const OP_SHORT: Record<string, string> = {
  open: "isolated — circuit broken, de-energized",
  close: "energized — circuit complete",
  trip: "auto-opened on a fault",
  lockout: "tripped; won't auto-reclose until reset",
  reclose: "auto-closed to test if the fault cleared",
  earth: "grounded for safe working",
  alarm: "indication only; no position change",
}

const LEGEND_TYPES = ["open", "close", "trip", "lockout", "reclose", "earth", "alarm"]

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

export function OpBadge({ type, className }: { type: string; className?: string }) {
  return (
    <span
      className={cn(
        "rounded px-1.5 py-0.5 text-xs font-medium capitalize",
        OP_BADGE[type] ?? "bg-muted text-muted-foreground",
        className,
      )}
    >
      {humanize(type)}
    </span>
  )
}

/** A one-time key explaining what each operation means — switching terms read
 * backwards to the uninitiated, so this sits above the log instead of nagging on
 * every row. */
export function OpLegend({ className }: { className?: string }) {
  return (
    <div className={cn("text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs", className)}>
      {LEGEND_TYPES.map((t) => (
        <span key={t} className="flex items-center gap-1.5">
          <OpBadge type={t} />
          <span>{OP_SHORT[t]}</span>
        </span>
      ))}
    </div>
  )
}
