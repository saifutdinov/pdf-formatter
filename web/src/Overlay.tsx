import { useRef, useState } from 'preact/hooks'
import { apply, deltaToUser, fromView, invert, objectQuad, rotateAround, spanAt, toView, translate, type Block, type EdObject, type Link, type PageInfo } from './api'

const area = (q: number[]) => Math.abs((q[2] - q[0]) * (q[7] - q[1]) - (q[3] - q[1]) * (q[6] - q[0]))

type Props = {
  page: PageInfo
  index: number
  scale: number
  blocks: Block[] | undefined
  links: Link[] | undefined
  selected: Set<string>
  editing: boolean
  nudge: [number, number] | null // pending arrow-key move of the selection, CSS px
  onSelect: (index: number, id: string | null, add: boolean) => void
  onTransform: (index: number, ids: string[], matrix: number[]) => void
  onEditText: (index: number, block: Block, cell?: [number, number]) => void
  onUpdate: (index: number, obj: EdObject) => void
  onFollow: (link: Link) => void
  guides?: number[] | null // margins in pt: top, right, bottom, left
}

type Drag =
  | { kind: 'cols'; obj: EdObject; k: number; next: EdObject }
  | { kind: 'move'; x: number; y: number; dx: number; dy: number; ghost: string; box: number[] }
  | { kind: 'rotate'; cx: number; cy: number; a0: number; angle: number }
  | { kind: 'resize'; corner: number; obj: EdObject; next: EdObject }

/** Resizes an object by dragging corner k (0 bl, 1 br, 2 tr, 3 tl) to local point (lx, ly). */
function resized(o: EdObject, k: number, lx: number, ly: number, keepAspect: boolean): EdObject {
  // Opposite corner stays fixed (local coordinates).
  const fixed = [
    [o.w, 0],
    [0, 0],
    [0, -o.h],
    [o.w, -o.h],
  ][k]
  let w = Math.max(4, Math.abs(lx - fixed[0]))
  let h = Math.max(4, Math.abs(ly - fixed[1]))
  if (keepAspect && o.w > 0 && o.h > 0) {
    const s = Math.max(w / o.w, h / o.h)
    w = o.w * s
    h = o.h * s
  }
  const left = k === 0 || k === 3 ? fixed[0] - w : fixed[0]
  const top = k === 2 || k === 3 ? fixed[1] + h : fixed[1]
  const [ex, ey] = apply(o.matrix, left, top)
  const m = [o.matrix[0], o.matrix[1], o.matrix[2], o.matrix[3], ex, ey]
  // Pulling a text box taller or shorter by hand turns automatic height off.
  return { ...o, matrix: m, w, h, autoHeight: o.autoHeight && Math.abs(h - o.h) < 1 }
}

/** Crops the rendered page canvas: the picture that follows the pointer. */
function snapshot(index: number, box: number[]): string {
  const canvas = document.querySelector<HTMLCanvasElement>(`.page[data-index="${index}"] canvas`)
  const pageEl = canvas?.closest<HTMLElement>('.page')
  if (!canvas || !pageEl) return ''
  const k = canvas.width / pageEl.clientWidth
  const [x0, y0, x1, y1] = box
  const w = Math.max(1, Math.round((x1 - x0) * k))
  const h = Math.max(1, Math.round((y1 - y0) * k))
  const c = document.createElement('canvas')
  c.width = w
  c.height = h
  c.getContext('2d')!.drawImage(canvas, x0 * k, y0 * k, w, h, 0, 0, w, h)
  return c.toDataURL()
}

/** SVG layer over a page: one polygon per block, correct for any rotation. */
export function PageOverlay({ page, index, scale, blocks, links, selected, editing, nudge, onSelect, onTransform, onEditText, onUpdate, onFollow, guides }: Props) {
  const [drag, setDrag] = useState<Drag | null>(null)
  const svg = useRef<SVGSVGElement>(null)
  // Double click detected by hand: the first click re-renders the polygon
  // into the selection group, so a native dblclick never fires.
  const lastDown = useRef({ id: '', t: 0 })
  if (!editing) {
    // View mode: links are live.
    if (!links?.length) return null
    return (
      <svg class="overlay view">
        {links.map((l) => {
          const pts = []
          for (let k = 0; k < 8; k += 2) pts.push(toView(page, scale, l.quad[k], l.quad[k + 1]).join(','))
          return (
            <polygon key={l.id} points={pts.join(' ')} class="lnk-live" onClick={() => onFollow(l)}>
              <title>{l.kind === 'uri' ? l.uri : 'Перейти к странице'}</title>
            </polygon>
          )
        })}
      </svg>
    )
  }
  if (!blocks) return null

  const quadPts = (q: number[]) => {
    const out: [number, number][] = []
    for (let k = 0; k < 8; k += 2) out.push(toView(page, scale, q[k], q[k + 1]))
    return out
  }
  const pts = (b: Block) => quadPts(b.quad)
  const sel = blocks.filter((b) => selected.has(`${index}:${b.id}`))
  const ids = sel.map((b) => b.id)
  // Selection box in screen space.
  let box = [Infinity, Infinity, -Infinity, -Infinity]
  for (const b of sel) for (const [x, y] of pts(b)) box = [Math.min(box[0], x), Math.min(box[1], y), Math.max(box[2], x), Math.max(box[3], y)]
  const cx = (box[0] + box[2]) / 2
  const cy = (box[1] + box[3]) / 2

  const local = (e: MouseEvent): [number, number] => {
    const r = svg.current!.getBoundingClientRect()
    return [e.clientX - r.left, e.clientY - r.top]
  }

  // Opens text editing for a text block or a table cell under the pointer.
  const openEdit = (e: MouseEvent, b: Block) => {
    if (b.kind === 'text') {
      onEditText(index, b)
      return true
    }
    if (b.kind === 'table' && b.object && b.rowHeights) {
      const [x, y] = local(e)
      const [ux, uy] = fromView(page, scale, x, y)
      const [lx, ly] = apply(invert(b.object.matrix), ux, uy)
      const cols = b.object.cols ?? []
      let c = 0
      for (let acc = 0; c < cols.length - 1 && lx > acc + cols[c]; c++) acc += cols[c]
      let r = 0
      for (let acc = 0; r < b.rowHeights.length - 1 && -ly > acc + b.rowHeights[r]; r++) acc += b.rowHeights[r]
      const s = spanAt(b.object, r, c) // a merged cell opens as one
      onEditText(index, b, [s.row, s.col])
      return true
    }
    return false
  }

  const startMove = (e: PointerEvent, b: Block) => {
    e.stopPropagation()
    const now = performance.now()
    if (lastDown.current.id === b.id && now - lastDown.current.t < 400 && !e.shiftKey && openEdit(e, b)) {
      lastDown.current = { id: '', t: 0 }
      return
    }
    lastDown.current = { id: b.id, t: now }
    const key = `${index}:${b.id}`
    if (!selected.has(key) || e.shiftKey) {
      onSelect(index, b.id, e.shiftKey)
      if (e.shiftKey) return
    }
    const moving = selected.has(key) ? sel : [b]
    let mb = [Infinity, Infinity, -Infinity, -Infinity]
    for (const m of moving) for (const [x, y] of pts(m)) mb = [Math.min(mb[0], x), Math.min(mb[1], y), Math.max(mb[2], x), Math.max(mb[3], y)]
    const [x, y] = local(e)
    svg.current!.setPointerCapture(e.pointerId)
    setDrag({ kind: 'move', x, y, dx: 0, dy: 0, ghost: snapshot(index, mb), box: mb })
  }

  const startRotate = (e: PointerEvent) => {
    e.stopPropagation()
    const [x, y] = local(e)
    svg.current!.setPointerCapture(e.pointerId)
    setDrag({ kind: 'rotate', cx, cy, a0: Math.atan2(y - cy, x - cx), angle: 0 })
  }

  const startResize = (e: PointerEvent, corner: number, obj: EdObject) => {
    e.stopPropagation()
    svg.current!.setPointerCapture(e.pointerId)
    setDrag({ kind: 'resize', corner, obj, next: obj })
  }

  const onMove = (e: PointerEvent) => {
    if (!drag) return
    const [x, y] = local(e)
    if (drag.kind === 'cols') {
      const [ux, uy] = fromView(page, scale, x, y)
      const [lx] = apply(invert(drag.obj.matrix), ux, uy)
      const cols = [...(drag.obj.cols ?? [])]
      const start = cols.slice(0, drag.k).reduce((a, b) => a + b, 0)
      const want = Math.max(10, lx - start)
      if (drag.k + 1 < cols.length) {
        // An inner border trades width between its two columns.
        const pair = cols[drag.k] + cols[drag.k + 1]
        cols[drag.k] = Math.min(want, pair - 10)
        cols[drag.k + 1] = pair - cols[drag.k]
      } else cols[drag.k] = want
      setDrag({ ...drag, next: { ...drag.obj, cols } })
    } else if (drag.kind === 'move') setDrag({ ...drag, dx: x - drag.x, dy: y - drag.y })
    else if (drag.kind === 'resize') {
      const [ux, uy] = fromView(page, scale, x, y)
      const [lx, ly] = apply(invert(drag.obj.matrix), ux, uy)
      setDrag({ ...drag, next: resized(drag.obj, drag.corner, lx, ly, drag.obj.kind === 'image' ? !e.shiftKey : e.shiftKey) })
    } else {
      let a = ((Math.atan2(y - drag.cy, x - drag.cx) - drag.a0) * 180) / Math.PI
      if (e.shiftKey) a = Math.round(a / 15) * 15
      setDrag({ ...drag, angle: a })
    }
  }

  const onUp = () => {
    if (!drag) return
    setDrag(null)
    if (drag.kind === 'resize' || drag.kind === 'cols') {
      if (drag.next !== drag.obj) onUpdate(index, drag.next)
    } else if (drag.kind === 'move') {
      if (Math.abs(drag.dx) + Math.abs(drag.dy) < 2) return
      const [ux, uy] = deltaToUser(page, scale, drag.dx, drag.dy)
      onTransform(index, ids, translate(ux, uy))
    } else if (Math.abs(drag.angle) > 0.2) {
      const [ux, uy] = fromView(page, scale, drag.cx, drag.cy)
      // Clockwise on screen is clockwise in user space: negative angle.
      onTransform(index, ids, rotateAround(-drag.angle, ux, uy))
    }
  }

  const shift = drag?.kind === 'move' ? [drag.dx, drag.dy] : drag?.kind === 'resize' || drag?.kind === 'cols' ? [0, 0] : (nudge ?? [0, 0])
  // Column borders of a selected table, as draggable lines.
  const tableSel = sel.length === 1 && sel[0].kind === 'table' && sel[0].object && sel[0].rowHeights ? sel[0] : null
  const colLines = (() => {
    if (!tableSel) return []
    const o = drag?.kind === 'cols' ? drag.next : tableSel.object!
    const h = tableSel.rowHeights!.reduce((a, b) => a + b, 0)
    let x = 0
    return (o.cols ?? []).map((w, k) => {
      x += w
      const a = toView(page, scale, ...apply(o.matrix, x, 0))
      const b = toView(page, scale, ...apply(o.matrix, x, -h))
      return { k, a, b }
    })
  })()
  const selTransform =
    drag?.kind === 'rotate' ? `rotate(${drag.angle} ${drag.cx} ${drag.cy})` : `translate(${shift[0]} ${shift[1]})`

  return (
    <svg
      ref={svg}
      class={`overlay ${drag ? 'dragging' : ''}`}
      onPointerDown={(e) => {
        if (e.target === e.currentTarget) onSelect(index, null, e.shiftKey)
      }}
      onPointerMove={onMove}
      onPointerUp={onUp}
      onPointerCancel={() => setDrag(null)}
    >
      {guides &&
        (() => {
          const [x0, y0, x1, y1] = page.box
          const q = [x0 + guides[3], y0 + guides[2], x1 - guides[1], y0 + guides[2], x1 - guides[1], y1 - guides[0], x0 + guides[3], y1 - guides[0]]
          return <polygon class="guides" points={quadPts(q).join(' ')} />
        })()}
      {/* Big blocks first so that small ones on top stay clickable. */}
      {[...blocks]
        .sort((a, b) => area(b.quad) - area(a.quad))
        .filter((b) => !selected.has(`${index}:${b.id}`))
        .map((b) => (
          <polygon
            key={b.id}
            points={pts(b).join(' ')}
            class={`blk ${b.kind}${b.invisible ? ' invisible' : ''}${b.object ? ' own' : ''}`}
            onPointerDown={(e) => startMove(e, b)}
          >
            <title>{b.text?.slice(0, 120) ?? b.kind}</title>
          </polygon>
        ))}
      {links?.map((l) => {
        const q = quadPts(l.quad)
        const sel = selected.has(`${index}:${l.id}`)
        return (
          <polygon
            key={l.id}
            points={q.join(' ')}
            class={`lnk${sel ? ' sel' : ''}`}
            onPointerDown={(e) => {
              e.stopPropagation()
              onSelect(index, l.id, e.shiftKey)
            }}
          >
            <title>{l.kind === 'uri' ? l.uri : 'Ссылка на страницу'}</title>
          </polygon>
        )
      })}
      {drag?.kind === 'move' && drag.ghost && (
        <>
          <rect class="ghost-hole" x={drag.box[0]} y={drag.box[1]} width={drag.box[2] - drag.box[0]} height={drag.box[3] - drag.box[1]} />
          <image
            href={drag.ghost}
            x={drag.box[0] + drag.dx}
            y={drag.box[1] + drag.dy}
            width={drag.box[2] - drag.box[0]}
            height={drag.box[3] - drag.box[1]}
            opacity={0.85}
          />
        </>
      )}
      {sel.length > 0 && (
        <g transform={selTransform}>
          {sel.map((b) => (
            <polygon
              key={b.id}
              points={(drag?.kind === 'resize' && drag.obj.id === b.id ? quadPts(objectQuad(drag.next)) : pts(b)).join(' ')}
              class={`blk ${b.kind} sel`}
              onPointerDown={(e) => startMove(e, b)}
              onDblClick={(e) => openEdit(e, b)}
            />
          ))}
          {colLines.map(({ k, a, b }) => (
            <line
              key={`c${k}`}
              class="col-handle"
              x1={a[0]}
              y1={a[1]}
              x2={b[0]}
              y2={b[1]}
              onPointerDown={(e) => {
                e.stopPropagation()
                svg.current!.setPointerCapture(e.pointerId)
                setDrag({ kind: 'cols', obj: tableSel!.object!, k, next: tableSel!.object! })
              }}
            />
          ))}
          {sel.length === 1 &&
            sel[0].object &&
            sel[0].kind !== 'table' &&
            (!drag || drag.kind === 'resize') &&
            pts(sel[0]).map(([x, y], k) => <rect key={k} class="size-handle" x={x - 4} y={y - 4} width={8} height={8} onPointerDown={(e) => startResize(e, k, sel[0].object!)} />)}
          {!drag && (
            <>
              <line class="rot-stem" x1={cx} y1={box[1]} x2={cx} y2={box[1] - 22} />
              <circle class="rot-handle" cx={cx} cy={box[1] - 26} r={6} onPointerDown={startRotate}>
                <title>Повернуть (Shift — шаг 15°)</title>
              </circle>
            </>
          )}
        </g>
      )}
    </svg>
  )
}
