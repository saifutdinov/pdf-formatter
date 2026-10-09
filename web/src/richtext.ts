import type { LibFont, Run } from './api'

/** A run with every style field filled in. */
export type StyledRun = Required<Omit<Run, 'underline'>> & { underline: boolean }

export const sameStyle = (a: StyledRun, b: StyledRun) =>
  a.font === b.font && a.size === b.size && a.color.toLowerCase() === b.color.toLowerCase() && a.underline === b.underline

/** Joins neighbours in the same style and drops empty runs. */
export function merge(runs: StyledRun[]): StyledRun[] {
  const out: StyledRun[] = []
  for (const r of runs) {
    if (!r.text) continue
    const last = out[out.length - 1]
    if (last && sameStyle(last, r)) last.text += r.text
    else out.push({ ...r })
  }
  return out
}

/** Fills in the object's style. */
export function resolve(runs: Run[] | undefined, text: string, base: { font: string; size: number; color: string }): StyledRun[] {
  const src = runs?.length ? runs : [{ text }]
  return src.map((r) => ({
    text: r.text,
    font: r.font || base.font,
    size: r.size || base.size,
    color: r.color || base.color,
    underline: !!r.underline,
  }))
}

/** Applies a change to the characters [start, end): runs are split at both ends. */
export function restyle(runs: StyledRun[], start: number, end: number, change: (r: StyledRun) => Partial<StyledRun>): StyledRun[] {
  const out: StyledRun[] = []
  let pos = 0
  for (const r of runs) {
    const a = Math.max(start - pos, 0)
    const b = Math.min(end - pos, r.text.length)
    if (a >= b) out.push(r)
    else {
      const mid = r.text.slice(a, b)
      if (a > 0) out.push({ ...r, text: r.text.slice(0, a) })
      out.push({ ...r, ...change({ ...r, text: mid }), text: mid })
      if (b < r.text.length) out.push({ ...r, text: r.text.slice(b) })
    }
    pos += r.text.length
  }
  return merge(out)
}

/** Runs within [start, end), or the run at the caret. */
export function within(runs: StyledRun[], start: number, end: number): StyledRun[] {
  const out: StyledRun[] = []
  let pos = 0
  for (const r of runs) {
    const next = pos + r.text.length
    if (start === end ? start > pos && start <= next : start < next && end > pos) out.push(r)
    pos = next
  }
  return out.length ? out : runs.slice(0, 1)
}

/** The face of the same family closest to bold and italic. */
export function variant(lib: LibFont[], id: string, bold: boolean, italic: boolean): LibFont | undefined {
  const base = lib.find((f) => f.id === id)
  if (!base) return undefined
  let best = base
  let score = -1
  for (const f of lib) {
    if (f.family !== base.family) continue
    const s = (f.bold === bold ? 2 : 0) + (f.italic === italic ? 1 : 0)
    if (s > score || (s === score && f.id === id)) [best, score] = [f, s]
  }
  return best
}

/**
 * Runs for the server: plain text when everything is in the object's
 * style, otherwise only the fields that differ from it.
 */
export function compact(runs: StyledRun[], base: { font: string; size: number; color: string }): { text: string; runs?: Run[] } {
  const text = runs.map((r) => r.text).join('')
  const plain = { font: base.font, size: base.size, color: base.color, underline: false, text: '' }
  if (runs.every((r) => sameStyle(r, plain))) return { text }
  return {
    text,
    runs: runs.map((r) => ({
      text: r.text,
      ...(r.font !== base.font && { font: r.font }),
      ...(r.size !== base.size && { size: r.size }),
      ...(r.color.toLowerCase() !== base.color.toLowerCase() && { color: r.color }),
      ...(r.underline && { underline: true }),
    })),
  }
}
