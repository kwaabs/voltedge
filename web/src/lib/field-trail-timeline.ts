// The time model behind the Field-trails playback timeline. Field sessions can sit
// hours or days apart, so a literal wall-clock axis would be mostly dead space. We
// keep true chronology and ordering but COLLAPSE idle gaps (stretches where no trail
// has any activity) down to a short fixed length, giving a "compressed" axis that is
// what the scrubber and playback run on. Two mappings convert between the compressed
// axis (uniform, what the user drags) and real epoch-ms (what the map reveals by).

/** A gap longer than this (ms of real time with no breadcrumb/edit anywhere) is
 * treated as idle. 2 min — longer than the ~45s breadcrumb cadence, so normal
 * movement is never collapsed, but between-session gaps are. */
const GAP_THRESHOLD_MS = 120_000
/** What an idle gap collapses to on the compressed axis (ms). Short, so playback
 * skips through dead time, but non-zero so the gap still reads as a small jump. */
const COLLAPSED_MS = 4_000

interface Segment {
  realStart: number
  realEnd: number
  compStart: number
  compEnd: number
}

export interface TrailTimeModel {
  realStart: number
  realEnd: number
  /** Total length of the compressed axis (ms). The scrubber runs 0..totalComp. */
  totalComp: number
  /** Compressed position → real epoch-ms. */
  compToReal: (comp: number) => number
  /** Real epoch-ms → compressed position (for placing pin ticks on the scrubber). */
  realToComp: (real: number) => number
}

/**
 * Builds the compressed time model from every activity instant (breadcrumb + edit
 * time, ms since epoch) across the shown trails. Returns null when there's nothing
 * to play (fewer than two distinct instants).
 */
export function buildTrailTimeModel(instantsMs: number[]): TrailTimeModel | null {
  const sorted = Array.from(new Set(instantsMs.filter((t) => Number.isFinite(t)))).sort((a, b) => a - b)
  if (sorted.length < 2) return null

  const segments: Segment[] = []
  let comp = 0
  for (let i = 0; i < sorted.length - 1; i++) {
    const realStart = sorted[i]
    const realEnd = sorted[i + 1]
    const realDur = realEnd - realStart
    const effDur = realDur > GAP_THRESHOLD_MS ? COLLAPSED_MS : realDur
    segments.push({ realStart, realEnd, compStart: comp, compEnd: comp + effDur })
    comp += effDur
  }

  const realStart = sorted[0]
  const realEnd = sorted[sorted.length - 1]
  const totalComp = comp

  function compToReal(c: number): number {
    const x = Math.max(0, Math.min(totalComp, c))
    for (const s of segments) {
      if (x <= s.compEnd) {
        const span = s.compEnd - s.compStart
        const f = span > 0 ? (x - s.compStart) / span : 0
        return s.realStart + f * (s.realEnd - s.realStart)
      }
    }
    return realEnd
  }

  function realToComp(t: number): number {
    if (t <= realStart) return 0
    if (t >= realEnd) return totalComp
    for (const s of segments) {
      if (t <= s.realEnd) {
        const span = s.realEnd - s.realStart
        const f = span > 0 ? (t - s.realStart) / span : 0
        return s.compStart + f * (s.compEnd - s.compStart)
      }
    }
    return totalComp
  }

  return { realStart, realEnd, totalComp, compToReal, realToComp }
}
