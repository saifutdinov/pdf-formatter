import type { ComponentChildren } from 'preact'
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks'
import { pageSize, toView, type PageInfo } from './api'
import { PageCanvas } from './PageCanvas'
import type { PDFDocumentProxy } from './pdf'

export const GAP = 16
const BUFFER = 2 // pages rendered above and below the visible ones

type Props = {
  pdf: PDFDocumentProxy
  pages: PageInfo[]
  scale: number
  version: number
  nav: { page: number; top?: number; seq: number } | null
  onPage: (index: number, offset: number) => void // page under the top third of the view; offset in CSS px from its top
  onWidth: (width: number) => void
  onZoomWheel?: (dir: 1 | -1) => void
  overlay?: (index: number, scale: number) => ComponentChildren
  overlayHTML?: (index: number, scale: number) => ComponentChildren
}

/** Continuous scroll over all pages; only pages near the viewport get a canvas. */
export function Viewer({ pdf, pages, scale, version, nav, onPage, onWidth, onZoomWheel, overlay, overlayHTML }: Props) {
  const scroller = useRef<HTMLDivElement>(null)
  const [range, setRange] = useState<[number, number]>([0, 0])
  const anchor = useRef({ index: 0, frac: 0 })

  const layout = useMemo(() => {
    const tops: number[] = []
    let y = GAP
    let maxW = 0
    for (const p of pages) {
      const [w, h] = pageSize(p)
      tops.push(y)
      y += h * scale + GAP
      maxW = Math.max(maxW, w * scale)
    }
    return { tops, height: y, width: maxW + 2 * GAP }
  }, [pages, scale])

  const pageAt = (y: number) => {
    let lo = 0
    let hi = layout.tops.length - 1
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (layout.tops[mid] <= y) lo = mid
      else hi = mid - 1
    }
    return lo
  }

  const update = () => {
    const el = scroller.current
    if (!el || !pages.length) return
    const first = pageAt(el.scrollTop)
    const last = pageAt(el.scrollTop + el.clientHeight)
    setRange([Math.max(0, first - BUFFER), Math.min(pages.length - 1, last + BUFFER)])
    const y = el.scrollTop + el.clientHeight / 3
    const i = pageAt(y)
    const h = pageSize(pages[i])[1] * scale
    anchor.current = { index: i, frac: Math.min(1, Math.max(0, (y - layout.tops[i]) / h)) }
    onPage(i, Math.max(0, el.scrollTop - layout.tops[i]))
  }

  // Keep the same spot under the eye when zooming.
  useLayoutEffect(() => {
    const el = scroller.current
    if (!el || !pages.length) return
    const { index, frac } = anchor.current
    const h = pageSize(pages[index])[1] * scale
    el.scrollTop = layout.tops[index] + frac * h - el.clientHeight / 3
    update()
  }, [layout])

  useLayoutEffect(() => {
    const el = scroller.current
    if (!el || !nav) return
    let off = 0
    if (nav.top !== undefined) {
      const p = pages[nav.page]
      off = Math.max(0, toView(p, scale, p.box[0], nav.top)[1] - 24)
    }
    el.scrollTop = layout.tops[nav.page] + off - GAP
    update()
  }, [nav])

  useEffect(() => {
    const el = scroller.current!
    const ro = new ResizeObserver(() => {
      onWidth(el.clientWidth)
      update()
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [layout])

  const onWheel = (e: WheelEvent) => {
    if (!(e.ctrlKey || e.metaKey) || !onZoomWheel) return
    e.preventDefault()
    onZoomWheel(e.deltaY < 0 ? 1 : -1)
  }

  const items = []
  for (let i = range[0]; i <= range[1] && i < pages.length; i++) {
    const [w, h] = pageSize(pages[i])
    items.push(
      <div
        key={i}
        class="page"
        data-index={i}
        style={{ top: `${layout.tops[i]}px`, width: `${w * scale}px`, height: `${h * scale}px`, left: `calc(50% - ${(w * scale) / 2}px)` }}
      >
        <PageCanvas pdf={pdf} index={i} scale={scale} version={version} />
        {overlay?.(i, scale)}
        {overlayHTML?.(i, scale)}
      </div>,
    )
  }

  return (
    <div class="viewer" ref={scroller} onScroll={update} onWheel={onWheel}>
      <div class="viewer-inner" style={{ height: `${layout.height}px`, width: `max(100%, ${layout.width}px)` }}>
        {items}
      </div>
    </div>
  )
}
