import { useEffect, useRef } from 'preact/hooks'
import type { PDFDocumentProxy } from './pdf'

const MAX_PIXELS = 16_000_000 // Safari refuses larger canvases

/**
 * Renders one page into a fresh canvas and swaps it in when done, so the old
 * picture stays visible while re-rendering (zoom, edits).
 */
export function PageCanvas({ pdf, index, scale, version = 0 }: { pdf: PDFDocumentProxy; index: number; scale: number; version?: number }) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let cancelled = false
    let task: { cancel(): void } | null = null
    pdf
      .getPage(index + 1)
      .then((page) => {
        if (cancelled) return
        const base = page.getViewport({ scale: 1 })
        let s = scale * Math.min(window.devicePixelRatio || 1, 2)
        if (base.width * base.height * s * s > MAX_PIXELS) s = Math.sqrt(MAX_PIXELS / (base.width * base.height))
        const viewport = page.getViewport({ scale: s })
        const canvas = document.createElement('canvas')
        canvas.width = Math.floor(viewport.width)
        canvas.height = Math.floor(viewport.height)
        const t = page.render({ canvas, viewport })
        task = t
        return t.promise.then(() => {
          if (!cancelled) box.current?.replaceChildren(canvas)
        })
      })
      .catch(() => {})
    return () => {
      cancelled = true
      task?.cancel()
    }
  }, [pdf, index, scale, version])
  return <div class="canvas-box" ref={box} />
}
