import { useLayoutEffect, useRef, useState } from 'preact/hooks'
import { toView, type LibFont, type PageInfo } from './api'
import { useLibFont } from './fontface'
import { merge, resolve, restyle, sameStyle, variant, within, type StyledRun } from './richtext'
import type { TextEdit } from './TextEditor'

const esc = (s: string) => s.replace(/[&<>"]/g, (c) => `&#${c.charCodeAt(0)};`)

/** HTML for runs: a span per run carrying its style in data attributes. */
function toHTML(runs: StyledRun[], scale: number): string {
  const html = runs
    .map((r) => {
      const style = [
        `font-family:${useLibFont(r.font).replace(/"/g, "'")}`,
        `font-size:${r.size * scale}px`,
        `color:${r.color}`,
        r.underline ? 'text-decoration:underline' : '',
      ].join(';')
      const body = r.text.split('\n').map(esc).join('<br>')
      return `<span data-f="${esc(r.font)}" data-s="${r.size}" data-c="${esc(r.color)}" data-u="${r.underline ? 1 : ''}" style="${style}">${body}</span>`
    })
    .join('')
  // A line break at the very end needs a second one to show the empty line.
  return runs.length && runs[runs.length - 1].text.endsWith('\n') ? html + '<br>' : html
}

/** Text nodes and line breaks in order, without the placeholder break at the end. */
function leaves(root: HTMLElement): Node[] {
  const out: Node[] = []
  const walk = (n: Node) => {
    for (const c of Array.from(n.childNodes)) {
      if (c.nodeType === Node.TEXT_NODE || c.nodeName === 'BR') out.push(c)
      else walk(c)
    }
  }
  walk(root)
  if (out.length && out[out.length - 1].nodeName === 'BR') out.pop()
  return out
}

function styleOf(root: HTMLElement, n: Node, base: StyledRun): StyledRun {
  const span = (n.nodeType === Node.TEXT_NODE ? n.parentElement : (n as Element))?.closest('[data-f]') as HTMLElement | null
  if (!span || !root.contains(span)) return { ...base, text: '' }
  const d = span.dataset
  return { text: '', font: d.f || base.font, size: Number(d.s) || base.size, color: d.c || base.color, underline: d.u === '1' }
}

/** Reads the runs back from the edited DOM (typing changes it directly). */
function readRuns(root: HTMLElement, base: StyledRun): StyledRun[] {
  return merge(
    leaves(root).map((n) => ({ ...styleOf(root, n, base), text: n.nodeName === 'BR' ? '\n' : ((n as Text).data ?? '').replace(/​/g, '') })),
  )
}

const leafLen = (n: Node) => (n.nodeName === 'BR' ? 1 : (n as Text).data.length)

/** Character offset of a DOM position. */
function offsetOf(root: HTMLElement, node: Node, off: number): number {
  const at = document.createRange()
  at.setStart(node, off)
  let pos = 0
  for (const l of leaves(root)) {
    if (l === node) return pos + off
    if (at.comparePoint(l, 0) < 0) pos += leafLen(l)
  }
  return pos
}

function domAt(root: HTMLElement, offset: number): [Node, number] {
  let pos = 0
  for (const l of leaves(root)) {
    const len = leafLen(l)
    if (l.nodeName === 'BR') {
      if (offset === pos) return [l.parentNode!, Array.from(l.parentNode!.childNodes).indexOf(l as ChildNode)]
    } else if (offset <= pos + len) return [l, offset - pos]
    pos += len
  }
  return [root, root.childNodes.length]
}

function selectionOffsets(root: HTMLElement): [number, number] | null {
  const sel = getSelection()
  if (!sel || !sel.rangeCount) return null
  const r = sel.getRangeAt(0)
  if (!root.contains(r.startContainer)) return null
  const a = offsetOf(root, r.startContainer, r.startOffset)
  const b = offsetOf(root, r.endContainer, r.endOffset)
  return [Math.min(a, b), Math.max(a, b)]
}

function select(root: HTMLElement, start: number, end: number) {
  const r = document.createRange()
  r.setStart(...domAt(root, start))
  r.setEnd(...domAt(root, end))
  const sel = getSelection()!
  sel.removeAllRanges()
  sel.addRange(r)
}

/**
 * Edits text in several styles laid over the block: bold, italic,
 * underline, colour and size apply to the selection (or to what is typed
 * next at the caret).
 */
export function RichTextEditor({
  edit,
  page,
  scale,
  library,
  onCommit,
  onCancel,
}: {
  edit: TextEdit
  page: PageInfo
  scale: number
  library: LibFont[]
  onCommit: (runs: StyledRun[]) => void
  onCancel: () => void
}) {
  const box = useRef<HTMLDivElement>(null)
  const bar = useRef<HTMLDivElement>(null)
  const base: StyledRun = { text: '', font: edit.font, size: edit.size, color: edit.color, underline: false }
  const initial = useRef(resolve(edit.runs, edit.text, base))
  const sel = useRef<[number, number]>([0, 0])
  const done = useRef(false)
  const [current, setCurrent] = useState<StyledRun>(initial.current[0] ?? base)

  const q = edit.quad
  const [blx, bly] = toView(page, scale, q[0], q[1])
  const [trx, try_] = toView(page, scale, q[4], q[5])
  const [tlx, tly] = toView(page, scale, q[6], q[7])
  const width = Math.hypot(trx - tlx, try_ - tly)
  const height = Math.hypot(blx - tlx, bly - tly)
  const angle = (Math.atan2(try_ - tly, trx - tlx) * 180) / Math.PI

  useLayoutEffect(() => {
    const el = box.current!
    el.innerHTML = toHTML(initial.current, scale)
    el.focus()
    const end = initial.current.reduce((n, r) => n + r.text.length, 0)
    select(el, end, end)
    sel.current = [end, end]
  }, [])

  const track = () => {
    const s = selectionOffsets(box.current!)
    if (!s) return
    sel.current = s
    const runs = readRuns(box.current!, base)
    setCurrent(within(runs, s[0], s[1])[0] ?? base)
  }

  // Restyles the selection: the DOM is rebuilt from the runs and the
  // selection put back.
  const apply = (change: (r: StyledRun) => Partial<StyledRun>) => {
    const el = box.current!
    const [a, b] = sel.current
    if (a === b) return
    const runs = restyle(readRuns(el, base), a, b, change)
    el.innerHTML = toHTML(runs, scale)
    el.focus()
    select(el, a, b)
    setCurrent(within(runs, a, b)[0] ?? base)
  }
  const fontOf = (id: string) => library.find((f) => f.id === id)
  const toggle = (key: 'bold' | 'italic') => {
    const runs = within(readRuns(box.current!, base), ...sel.current)
    const on = !runs.every((r) => fontOf(r.font)?.[key])
    apply((r) => {
      const f = fontOf(r.font)
      const v = f && variant(library, r.font, key === 'bold' ? on : f.bold, key === 'italic' ? on : f.italic)
      return v && v[key] === on ? { font: v.id } : {}
    })
  }
  const underline = () => {
    const runs = within(readRuns(box.current!, base), ...sel.current)
    const on = !runs.every((r) => r.underline)
    apply(() => ({ underline: on }))
  }

  const commit = () => {
    if (done.current) return
    done.current = true
    const runs = readRuns(box.current!, base)
    const same = runs.length === initial.current.length && runs.every((r, i) => r.text === initial.current[i].text && sameStyle(r, initial.current[i]))
    same ? onCancel() : onCommit(runs)
  }
  const cur = fontOf(current.font)
  const p = edit.padding.map((v) => v * scale)
  const keep = (e: Event) => e.preventDefault() // buttons keep the selection in the text

  return (
    <>
      <div
        ref={bar}
        class="rich-bar"
        style={{ left: `${tlx}px`, top: `${tly - 34}px` }}
        onPointerDown={(e) => e.stopPropagation()}
        onMouseDown={(e) => (e.target as HTMLElement).tagName !== 'INPUT' && keep(e)}
        onFocusOut={(e) => {
          const to = e.relatedTarget as Node | null
          if (!box.current?.contains(to) && !bar.current?.contains(to)) commit()
        }}
      >
        <button class={`btn small ${cur?.bold ? 'active' : ''}`} title="Жирный (Ctrl+B)" onClick={() => toggle('bold')}>
          <b>Ж</b>
        </button>
        <button class={`btn small ${cur?.italic ? 'active' : ''}`} title="Курсив (Ctrl+I)" onClick={() => toggle('italic')}>
          <i>К</i>
        </button>
        <button class={`btn small ${current.underline ? 'active' : ''}`} title="Подчёркнутый (Ctrl+U)" onClick={underline}>
          <u>Ч</u>
        </button>
        <input type="color" title="Цвет выделенного" value={current.color} onInput={(e) => apply(() => ({ color: e.currentTarget.value }))} />
        <input
          class="num-input"
          title="Кегль выделенного, pt"
          value={String(Math.round(current.size * 10) / 10).replace('.', ',')}
          key={current.size}
          onKeyDown={(e) => {
            e.stopPropagation()
            if (e.key !== 'Enter') return
            const v = Number(e.currentTarget.value.replace(',', '.'))
            if (v > 0 && v < 500) apply(() => ({ size: v }))
          }}
        />
        <span class="muted small">pt</span>
      </div>
      <div
        ref={box}
        class="text-editor rich"
        contentEditable
        spellcheck
        style={{
          left: `${tlx}px`,
          top: `${tly}px`,
          width: `${Math.max(width, 40)}px`,
          minHeight: `${height}px`,
          transform: `rotate(${angle}deg)`,
          lineHeight: edit.lineHeight,
          textAlign: edit.align === 'justify' ? 'justify' : (edit.align as 'left'),
          padding: `${p[0]}px ${p[1]}px ${p[2]}px ${p[3]}px`,
        }}
        onKeyUp={track}
        onMouseUp={track}
        onBlur={(e) => !bar.current?.contains(e.relatedTarget as Node) && commit()}
        onPointerDown={(e) => e.stopPropagation()}
        onPaste={(e) => {
          e.preventDefault()
          document.execCommand('insertText', false, e.clipboardData?.getData('text/plain') ?? '')
        }}
        onKeyDown={(e) => {
          e.stopPropagation()
          const mod = e.ctrlKey || e.metaKey
          if (e.key === 'Escape') {
            done.current = true
            onCancel()
          } else if (e.key === 'Enter' && mod) commit()
          else if (e.key === 'Enter') {
            e.preventDefault()
            document.execCommand('insertLineBreak')
          } else if (mod && ['b', 'i', 'u', 'и', 'ш', 'г'].includes(e.key.toLowerCase())) {
            e.preventDefault()
            track()
            const k = e.key.toLowerCase()
            if (k === 'b' || k === 'и') toggle('bold')
            else if (k === 'i' || k === 'ш') toggle('italic')
            else underline()
          }
        }}
      />
    </>
  )
}
