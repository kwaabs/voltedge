import { Pause, Play, RotateCcw } from "lucide-react"

import type { TrailTimeModel } from "@/lib/field-trail-timeline"
import { cn } from "@/lib/utils"

/** One edit's mark on the scrubber: its fractional position (0..1) and trail colour. */
export interface TimelineTick {
  position: number
  color: string
}

const SPEEDS = [5, 20, 60, 120]

function fmtClock(ms: number, multiDay: boolean): string {
  const d = new Date(ms)
  if (Number.isNaN(d.getTime())) return "—"
  return multiDay ? d.toLocaleString() : d.toLocaleTimeString()
}

/** The playback timeline strip under the trail map: play/pause, a speed selector, a
 * draggable scrubber with a tick per edit (coloured by trail), and a live clock. It
 * runs on the compressed axis (idle gaps collapsed — see lib/field-trail-timeline). */
export function FieldTrackTimeline({
  model,
  playheadComp,
  playing,
  speed,
  ticks,
  onScrub,
  onPlayPause,
  onRestart,
  onSpeed,
}: {
  model: TrailTimeModel
  playheadComp: number
  playing: boolean
  speed: number
  ticks: TimelineTick[]
  onScrub: (comp: number) => void
  onPlayPause: () => void
  onRestart: () => void
  onSpeed: (speed: number) => void
}) {
  const frac = model.totalComp > 0 ? playheadComp / model.totalComp : 0
  const multiDay = model.realEnd - model.realStart > 24 * 3600_000
  const nowReal = model.compToReal(playheadComp)

  return (
    <div className="flex items-center gap-3 rounded-lg border px-3 py-2">
      <button
        type="button"
        onClick={onPlayPause}
        className="bg-primary text-primary-foreground flex size-8 shrink-0 items-center justify-center rounded-full"
        title={playing ? "Pause" : "Play"}
      >
        {playing ? <Pause className="size-4" /> : <Play className="ml-0.5 size-4" />}
      </button>
      <button
        type="button"
        onClick={onRestart}
        className="text-muted-foreground hover:text-foreground shrink-0"
        title="Restart"
      >
        <RotateCcw className="size-4" />
      </button>

      {/* Scrubber: track + fill + per-edit ticks + handle, with an invisible native
          range on top for drag/keyboard. */}
      <div className="relative h-6 flex-1">
        <div className="bg-muted absolute left-0 right-0 top-1/2 h-1.5 -translate-y-1/2 rounded-full" />
        <div
          className="bg-primary/70 absolute left-0 top-1/2 h-1.5 -translate-y-1/2 rounded-full"
          style={{ width: `${frac * 100}%` }}
        />
        {ticks.map((t, i) => (
          <span
            key={i}
            className="absolute top-1/2 h-3 w-0.5 -translate-x-1/2 -translate-y-1/2 rounded-full"
            style={{ left: `${t.position * 100}%`, backgroundColor: t.color }}
          />
        ))}
        <div
          className="border-primary absolute top-1/2 size-3.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 bg-white shadow"
          style={{ left: `${frac * 100}%` }}
        />
        <input
          type="range"
          min={0}
          max={model.totalComp}
          step={Math.max(1, Math.round(model.totalComp / 1000))}
          value={playheadComp}
          onChange={(e) => onScrub(Number(e.target.value))}
          className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
          aria-label="Timeline position"
        />
      </div>

      <span className="text-muted-foreground w-28 shrink-0 text-right text-xs tabular-nums" title="Current time">
        {fmtClock(nowReal, multiDay)}
      </span>

      <select
        value={speed}
        onChange={(e) => onSpeed(Number(e.target.value))}
        className={cn("bg-background shrink-0 rounded-md border px-1.5 py-1 text-xs")}
        title="Playback speed"
      >
        {SPEEDS.map((s) => (
          <option key={s} value={s}>
            {s}×
          </option>
        ))}
      </select>
    </div>
  )
}
