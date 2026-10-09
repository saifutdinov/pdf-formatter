import { useEffect, useRef, useState } from 'preact/hooks'
import { api, pageSize, type Inspected, type PageInfo } from './api'
import { PageCanvas } from './PageCanvas'
import type { PDFDocumentProxy } from './pdf'

const THUMB_W = 140

export type PageActions = {
  rotate: (index: number, deg: number) => void
  remove: (index: number) => void
  insertAfter: (index: number) => void
  move: (from: number, to: number) => void // to: index of the page to drop after, -1 = first
  importPdf: (file: File) => void // pages of another PDF after the current one
}

function Thumb({
  pdf,
  index,
  page,
  active,
  onSelect,
  actions,
  count,
}: {
  pdf: PDFDocumentProxy
  index: number
  page: PageInfo
  active: boolean
  onSelect: () => void
  actions?: PageActions
  count: number
}) {
  const el = useRef<HTMLButtonElement>(null)
  const [seen, setSeen] = useState(false)
  useEffect(() => {
    const io = new IntersectionObserver(([e]) => e.isIntersecting && setSeen(true), { rootMargin: '300px' })
    io.observe(el.current!)
    return () => io.disconnect()
  }, [])
  useEffect(() => {
    if (active) el.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  const [over, setOver] = useState(false)
  const [w, h] = pageSize(page)
  const scale = THUMB_W / w
  return (
    <button
      ref={el}
      class={`thumb ${active ? 'active' : ''} ${over ? 'drop' : ''}`}
      onClick={onSelect}
      draggable={!!actions}
      onDragStart={(e) => e.dataTransfer?.setData('text/page', String(index))}
      onDragOver={(e) => {
        if (!actions) return
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault()
        setOver(false)
        const from = Number(e.dataTransfer?.getData('text/page'))
        if (actions && Number.isFinite(from) && from !== index) actions.move(from, index)
      }}
    >
      <div class="thumb-page" style={{ width: `${THUMB_W}px`, height: `${h * scale}px` }}>
        {seen && <PageCanvas pdf={pdf} index={index} scale={scale} />}
        {actions && (
          <span class="thumb-actions" onClick={(e) => e.stopPropagation()}>
            <span title="Повернуть влево" onClick={() => actions.rotate(index, -90)}>
              ⟲
            </span>
            <span title="Повернуть вправо" onClick={() => actions.rotate(index, 90)}>
              ⟳
            </span>
            <span title="Вставить пустую страницу после" onClick={() => actions.insertAfter(index)}>
              ＋
            </span>
            {count > 1 && (
              <span title="Удалить страницу" onClick={() => confirm(`Удалить страницу ${index + 1}?`) && actions.remove(index)}>
                ✕
              </span>
            )}
          </span>
        )}
      </div>
      <span>{page.label || index + 1}</span>
    </button>
  )
}

export function Thumbs({ pdf, pages, current, onNav, actions }: { pdf: PDFDocumentProxy; pages: PageInfo[]; current: number; onNav: (i: number) => void; actions?: PageActions }) {
  return (
    <div class="thumbs">
      {actions && (
        <div class="thumbs-tools">
          <label class="btn small">
            Вставить страницы из PDF…
            <input
              type="file"
              accept="application/pdf,.pdf"
              hidden
              onChange={(e) => {
                const f = e.currentTarget.files?.[0]
                if (f) actions.importPdf(f)
                e.currentTarget.value = ''
              }}
            />
          </label>
          <p class="muted small thumbs-hint">Перетащите миниатюру, чтобы переставить страницу.</p>
        </div>
      )}
      {pages.map((p, i) => (
        <Thumb key={`${p.num}`} pdf={pdf} index={i} page={p} active={i === current} onSelect={() => onNav(i)} actions={actions} count={pages.length} />
      ))}
    </div>
  )
}

function linkify(text: string, open: (n: number) => void) {
  const parts = []
  const re = /(\d+) (\d+) R/g
  let last = 0
  for (let m; (m = re.exec(text)); ) {
    parts.push(text.slice(last, m.index))
    const n = Number(m[1])
    parts.push(
      <a key={m.index} onClick={() => open(n)}>
        {m[0]}
      </a>,
    )
    last = m.index + m[0].length
  }
  parts.push(text.slice(last))
  return parts
}

export function Inspector({ id, pageNum }: { id: string; pageNum?: number }) {
  const [stack, setStack] = useState<(number | 'trailer')[]>(['trailer'])
  const [obj, setObj] = useState<Inspected | null>(null)
  const [error, setError] = useState('')
  const cur = stack[stack.length - 1]
  useEffect(() => {
    setError('')
    api.object(id, cur).then(setObj).catch((e) => setError(e.message))
  }, [id, cur])
  const open = (n: number | 'trailer') => setStack((s) => [...s, n])
  return (
    <div class="inspector">
      <div class="inspector-bar">
        <button class="btn small" disabled={stack.length < 2} onClick={() => setStack((s) => s.slice(0, -1))}>
          ←
        </button>
        <button class="btn small" onClick={() => open('trailer')}>
          Трейлер
        </button>
        {pageNum !== undefined && (
          <button class="btn small" onClick={() => open(pageNum)}>
            Страница
          </button>
        )}
        <input
          class="num-input"
          placeholder="№"
          onKeyDown={(e) => {
            const v = Number(e.currentTarget.value)
            if (e.key === 'Enter' && v > 0) open(v)
          }}
        />
      </div>
      {error && <div class="banner error">{error}</div>}
      {obj && (
        <div class="inspector-body">
          <div class="muted">
            {obj.ref} · {obj.type}
          </div>
          <pre>{linkify(obj.text, open)}</pre>
          {obj.decoded !== undefined && (
            <details>
              <summary>
                Данные потока: {obj.length} байт{obj.error ? ` (${obj.error})` : ''}
              </summary>
              <pre>{obj.decoded}</pre>
            </details>
          )}
        </div>
      )}
    </div>
  )
}
