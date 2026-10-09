import { useEffect, useRef, useState } from 'preact/hooks'
import {
  api,
  ApiError,
  passwords,
  blocksBox,
  apply,
  cellRect,
  deltaToUser,
  fromView,
  MM,
  pageSize,
  rotateAround,
  translate,
  objectQuad,
  type Block,
  type DocInfo,
  type EdObject,
  type EditOp,
  type EditState,
  type FontInfo,
  type FormField,
  type LibFont,
  type Link,
  type PageInfo,
  type Progress,
} from './api'
import { PageOverlay } from './Overlay'
import { DocPanel } from './DocPanel'
import { FontsPanel } from './Panels'
import { Properties } from './Properties'
import { loadPdf, type PDFDocumentProxy } from './pdf'
import { LayoutPanel } from './LayoutPanel'
import { AttachLink, LinkProps } from './Links'
import { OutlineEditor } from './OutlineEditor'
import { PasswordDialog } from './PasswordDialog'
import { FormLayer } from './FormLayer'
import { Inspector, Thumbs } from './Sidebar'
import { TextEditor, type TextEdit } from './TextEditor'
import { RichTextEditor } from './RichTextEditor'
import { compact, variant, type StyledRun } from './richtext'
import { GAP, Viewer } from './Viewer'

/** Same rule as the server: a single unrotated line may grow to the mirrored right margin. */
function widenSingleLine(b: Block, p: PageInfo): number[] {
  const q = b.quad
  if ((b.text ?? '').includes('\n') || Math.abs(b.angle ?? 0) >= 0.5) return q
  const left = q[6]
  const right = p.box[2] - (left - p.box[0])
  if (right <= q[4]) return q
  return [q[0], q[1], right, q[3], right, q[5], q[6], q[7]]
}

const ZOOMS = [0.25, 0.33, 0.5, 0.67, 0.75, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3, 4, 5]
type Tab = 'pages' | 'outline' | 'layout' | 'fonts' | 'doc' | 'objects'

type PageData = { blocks: Block[]; links: Link[] }

/** Loads blocks and links per page on demand and caches them. */
function useBlocks(id: string) {
  const cache = useRef(new Map<number, PageData>())
  const pending = useRef(new Map<number, number>()) // page → request generation
  const gen = useRef(0)
  const [, bump] = useState(0)
  const load = (page: number) => {
    const g = ++gen.current
    pending.current.set(page, g)
    api
      .blocks(id, page)
      .then((r) => {
        if (pending.current.get(page) !== g) return // superseded by a newer request
        cache.current.set(page, { blocks: r.blocks, links: r.links ?? [] })
        bump((n) => n + 1)
      })
      .finally(() => {
        if (pending.current.get(page) === g) pending.current.delete(page)
      })
  }
  const data = (page: number) => {
    const b = cache.current.get(page)
    if (!b && !pending.current.has(page)) load(page)
    return b
  }
  const get = (page: number) => data(page)?.blocks
  const links = (page: number) => data(page)?.links
  // Refetch, keeping the old blocks visible until the new ones arrive.
  const refresh = (pages: number[]) => pages.forEach(load)
  // Page indices shifted: forget everything.
  const reset = () => {
    cache.current.clear()
    pending.current.clear()
    bump((n) => n + 1)
  }
  return { get, links, refresh, reset }
}

function useFonts(id: string) {
  const [fonts, setFonts] = useState<FontInfo[]>([])
  const [progress, setProgress] = useState<Progress | null>(null)
  useEffect(() => {
    const reload = () => api.fonts(id).then((r) => setFonts(r.fonts))
    reload()
    const es = new EventSource(`/api/docs/${id}/events`)
    let timer = 0
    es.addEventListener('progress', (e) => {
      const p = JSON.parse((e as MessageEvent).data) as Progress
      setProgress(p)
      if (p.finished) reload()
    })
    const later = () => {
      clearTimeout(timer)
      timer = setTimeout(reload, 300)
    }
    es.addEventListener('font', later)
    es.addEventListener('fonts', later) // library changed: matches may differ
    return () => {
      es.close()
      clearTimeout(timer)
    }
  }, [id])
  return { fonts, progress }
}

/** Asks for the password of a locked document before the editor mounts. */
export function Editor({ id }: { id: string }) {
  const [locked, setLocked] = useState<boolean | null>(null)
  useEffect(() => {
    setLocked(null)
    api
      .doc(id)
      .then(() => setLocked(false))
      .catch((e) => setLocked(e instanceof ApiError && e.status === 423))
  }, [id])
  if (locked === null) return null
  if (locked) return <PasswordDialog id={id} owner={false} onDone={() => setLocked(false)} onCancel={() => (location.hash = '#/')} />
  return <EditorView id={id} />
}

function EditorView({ id }: { id: string }) {
  const [info, setInfo] = useState<DocInfo | null>(null)
  const [pdf, setPdf] = useState<PDFDocumentProxy | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [zoom, setZoom] = useState<number | 'fit'>('fit')
  const [width, setWidth] = useState(800)
  const [page, setPage] = useState(0)
  const [pageOffset, setPageOffset] = useState(0)
  const [nav, setNav] = useState<{ page: number; top?: number; seq: number } | null>(null)
  const [tab, setTab] = useState<Tab>('pages')
  const [editing, setEditing] = useState(true)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bannerHidden, setBannerHidden] = useState(false)
  // Encrypted documents: 'open' asks for the password, 'owner' for the
  // owner password that lifts the restrictions.
  const [ask, setAsk] = useState<'open' | 'owner' | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [state, setState] = useState<EditState | null>(null)
  const [nudge, setNudge] = useState<{ page: number; ids: string[]; dx: number; dy: number } | null>(null)
  const nudgeTimer = useRef(0)
  const task = useRef<ReturnType<typeof loadPdf> | null>(null)
  const [library, setLibrary] = useState<LibFont[]>([])
  const [defaultFont, setDefaultFont] = useState('')
  const [textEdit, setTextEdit] = useState<TextEdit | null>(null)
  const [guides, setGuidesState] = useState<number[] | null>(() => {
    try {
      return JSON.parse(localStorage.getItem(`guides:${id}`) ?? 'null')
    } catch {
      return null
    }
  })
  const setGuides = (g: number[] | null) => {
    setGuidesState(g)
    try {
      localStorage.setItem(`guides:${id}`, JSON.stringify(g))
    } catch {
      // private mode: guides just are not remembered
    }
  }
  const blocks = useBlocks(id)
  const blocksOf = blocks.get
  const { fonts, progress } = useFonts(id)

  // Each revision is a new URL; the old document stays on screen until the
  // new one is ready, so edits do not flicker. Loads may finish out of
  // order: only the latest requested revision is shown.
  const latest = useRef(0)
  const loadRev = (rev: number) => {
    const seq = ++latest.current
    const t = loadPdf(api.fileUrl(id, rev), passwords.get(id))
    t.promise
      .then((d) => {
        if (seq !== latest.current) return t.destroy()
        const old = task.current
        task.current = t
        setPdf(d)
        if (old && old !== t) setTimeout(() => old.destroy(), 2000)
      })
      .catch((e) => {
        if (seq !== latest.current) return
        // The server has the password, this tab not yet (e.g. after a reload).
        if (e.name === 'PasswordException') setAsk('open')
        else setError(`pdf.js: ${e.message}`)
      })
  }

  useEffect(() => {
    api.library().then((r) => {
      setLibrary(r.fonts)
      setDefaultFont(r.default)
    })
  }, [])

  useEffect(() => {
    api
      .doc(id)
      .then((d) => {
        setInfo(d)
        setState(d.state)
        loadRev(d.state.rev)
      })
      .catch((e) => setError(e.message))
    return () => {
      task.current?.destroy()
    }
  }, [id, reloadKey])

  // Form fields, filled in view mode; refreshed with every revision.
  const [form, setForm] = useState<FormField[] | null>(null)
  const [formHint, setFormHint] = useState(true)
  useEffect(() => {
    api
      .fields(id)
      .then((r) => setForm(r.flattened || !r.fields.length ? null : r.fields))
      .catch(() => setForm(null))
  }, [id, state?.rev])

  // Restricted documents open for viewing; changes need the owner password.
  const readonly = info?.security ? !info.security.editable : false
  useEffect(() => {
    if (readonly) setEditing(false)
  }, [readonly])

  const pages = info?.pages ?? []
  const maxW = Math.max(1, ...pages.map((p) => pageSize(p)[0]))
  const fit = Math.max(0.1, (width - 2 * GAP) / maxW)
  const scale = zoom === 'fit' ? fit : zoom
  const go = (p: number, top?: number) => setNav({ page: Math.max(0, Math.min(pages.length - 1, p)), top, seq: Date.now() })
  const follow = (l: Link) => {
    if (l.kind === 'uri' && l.uri) window.open(l.uri, '_blank', 'noopener')
    else if (l.kind === 'page') go(pages.findIndex((p) => p.num === l.page), l.top)
  }
  const step = (dir: 1 | -1) => {
    const next = dir > 0 ? ZOOMS.find((z) => z > scale + 0.001) : [...ZOOMS].reverse().find((z) => z < scale - 0.001)
    if (next) setZoom(next)
  }

  const applyState = (st: EditState) => {
    setState(st)
    loadRev(st.rev)
    if (st.structure) {
      setSelected(new Set())
      blocks.reset()
      api.doc(id).then(setInfo)
    } else if (st.pages?.length) blocks.refresh(st.pages)
  }
  const run = (p: Promise<EditState>) =>
    p.then(applyState).catch((e) => {
      setError(e.message)
      setTimeout(() => setError(''), 5000)
    })
  const editOp = (pageIndex: number, op: Omit<EditOp, 'page'>) => run(api.edit(id, { ...op, page: pages[pageIndex].num }))
  const pageActions = {
    rotate: (i: number, deg: number) => editOp(i, { op: 'page.rotate', rotate: deg }),
    remove: (i: number) => editOp(i, { op: 'page.delete' }),
    insertAfter: (i: number) => {
      const b = pages[i].box
      run(api.edit(id, { op: 'page.insert', page: 0, after: pages[i].num, size: [b[2] - b[0], b[3] - b[1]] }))
    },
    importPdf: (file: File) =>
      api
        .uploadAsset(id, file)
        .then(({ asset, kind }) => {
          if (kind !== 'pdf') throw new Error('Это не PDF')
          return run(api.edit(id, { op: 'page.import', page: 0, asset, after: pages[page]?.num ?? 0 }))
        })
        .catch((e) => setError(e.message)),
    move: (from: number, to: number) =>
      editOp(from, { op: 'page.move', after: from < to ? pages[to].num : to === 0 ? 0 : pages[to - 1].num }),
  }
  // Font, size or colour of a whole text object carry over to its styled
  // runs: bold stays bold in the new family, sizes keep their proportion.
  const updateObject = (pageIndex: number, object: EdObject) => {
    const old = (blocksOf(pageIndex) ?? []).find((x) => x.object?.id === object.id)?.object
    if (old?.runs && object.runs === old.runs) {
      const scale = old.size && object.size ? object.size / old.size : 1
      object = {
        ...object,
        runs: object.runs.map((r) => {
          const f = r.font && library.find((x) => x.id === r.font)
          const font = object.font !== old.font && f ? variant(library, object.font || defaultFont, f.bold, f.italic)?.id : r.font
          return {
            ...r,
            font: font === object.font ? undefined : font,
            size: r.size && Math.round(r.size * scale * 10) / 10,
            color: object.color !== old.color ? undefined : r.color,
          }
        }),
      }
    }
    return editOp(pageIndex, { op: 'update', object })
  }

  const cellEdit = (pageIndex: number, b: Block, o: EdObject, rowHeights: number[], r: number, c: number): TextEdit => {
    const [x, y, w, h] = cellRect(o, rowHeights, r, c)
    const quad = [...apply(o.matrix, x, y - h), ...apply(o.matrix, x + w, y - h), ...apply(o.matrix, x + w, y), ...apply(o.matrix, x, y)]
    const pad = o.cellPad ?? 4
    return {
      page: pageIndex,
      blockId: b.id,
      own: true,
      text: o.rows?.[r]?.cells[c] ?? '',
      font: o.font || defaultFont,
      size: o.size ?? 10,
      color: o.color ?? '#000000',
      align: o.align ?? 'left',
      lineHeight: o.lineHeight || 1.2,
      padding: [pad, pad, pad, pad],
      quad,
      cell: [r, c],
      obj: o,
    }
  }

  const setCell = (o: EdObject, r: number, c: number, text: string): EdObject => {
    const rows = (o.rows ?? []).map((row) => ({ ...row, cells: [...row.cells] }))
    while (rows.length <= r) rows.push({ cells: (o.cols ?? []).map(() => '') })
    while (rows[r].cells.length <= c) rows[r].cells.push('')
    rows[r].cells[c] = text
    return { ...o, rows }
  }

  // Tab moves to the next cell (Shift+Tab back); Tab in the last cell adds a row.
  const tabCell = (te: TextEdit, text: string, back: boolean) => {
    const b = (blocksOf(te.page) ?? []).find((x) => x.id === te.blockId)
    const cur = te.obj ?? b?.object
    if (!b || !cur || !te.cell) return setTextEdit(null)
    const o = setCell(cur, te.cell[0], te.cell[1], text)
    const cols = o.cols?.length ?? 1
    let idx = te.cell[0] * cols + te.cell[1] + (back ? -1 : 1)
    idx = Math.max(0, idx)
    const r = Math.floor(idx / cols)
    const next = r >= (o.rows?.length ?? 0) ? setCell(o, r, 0, '') : o
    updateObject(te.page, next)
    const heights = [...(b.rowHeights ?? [])]
    while (heights.length <= r) heights.push(heights[heights.length - 1] ?? 20)
    setTextEdit(cellEdit(te.page, b, next, heights, r, idx % cols))
  }

  const editText = (pageIndex: number, b: Block, cell?: [number, number]) => {
    const o = b.object
    if (cell && o && b.rowHeights) return setTextEdit(cellEdit(pageIndex, b, o, b.rowHeights, cell[0], cell[1]))
    if (b.kind === 'table') return
    setTextEdit(
      o
        ? {
            page: pageIndex,
            blockId: b.id,
            own: true,
            text: o.text ?? '',
            runs: o.runs,
            font: o.font || defaultFont,
            size: o.size ?? 12,
            color: o.color ?? '#000000',
            align: o.align ?? 'left',
            lineHeight: o.lineHeight || 1.2,
            padding: o.padding ?? [0, 0, 0, 0],
            quad: b.quad,
          }
        : {
            page: pageIndex,
            blockId: b.id,
            own: false,
            quad: widenSingleLine(b, pages[pageIndex]),
            text: b.flow ?? b.text ?? '',
            runs: b.runs,
            font: b.libFont ?? defaultFont,
            size: b.size ?? 12,
            color: b.color ?? '#000000',
            align: 'left',
            lineHeight: 1.2,
            padding: [0, 0, 0, 0],
          },
    )
  }

  // Text in several styles: plain text when it is all in the block's style.
  const commitRich = (te: TextEdit, runs: StyledRun[]) => {
    setTextEdit(null)
    const { text, runs: styled } = compact(runs, te)
    if (te.own) {
      const b = (blocksOf(te.page) ?? []).find((x) => x.id === te.blockId)
      if (b?.object) updateObject(te.page, { ...b.object, text, runs: styled })
    } else {
      editOp(te.page, { op: 'adopt', blocks: [te.blockId], object: { text, runs: styled } as EdObject }).then(() => setSelected(new Set()))
    }
  }

  const commitText = (te: TextEdit, text: string) => {
    setTextEdit(null)
    if (te.cell) {
      const b = (blocksOf(te.page) ?? []).find((x) => x.id === te.blockId)
      const cur = te.obj ?? b?.object
      if (cur) updateObject(te.page, setCell(cur, te.cell[0], te.cell[1], text))
    } else if (te.own) {
      const b = (blocksOf(te.page) ?? []).find((x) => x.id === te.blockId)
      if (b?.object) updateObject(te.page, { ...b.object, text })
    } else {
      editOp(te.page, { op: 'adopt', blocks: [te.blockId], object: { text } }).then(() => setSelected(new Set()))
    }
  }

  // New objects go to the middle of the current page.
  const addObject = (o: Omit<EdObject, 'id' | 'matrix'>, thenEdit = false) => {
    const p = pages[page]
    if (!p) return
    const cx = (p.box[0] + p.box[2]) / 2
    const cy = (p.box[1] + p.box[3]) / 2
    const object = { ...o, matrix: [1, 0, 0, 1, cx - o.w / 2, cy + o.h / 2] }
    api
      .edit(id, { op: 'add', page: p.num, object })
      .then((st) => {
        applyState(st)
        if (!st.created) return
        setSelected(new Set([`${page}:${st.created}`]))
        if (thenEdit && o.kind === 'text')
          setTextEdit({
            page,
            blockId: st.created,
            own: true,
            text: o.text ?? '',
            font: o.font || defaultFont,
            size: o.size ?? 12,
            color: o.color ?? '#000000',
            align: o.align ?? 'left',
            lineHeight: 1.2,
            padding: o.padding,
            quad: objectQuad(object),
          })
      })
      .catch((e) => setError(e.message))
  }

  const svgNotice = (warnings?: string[]) => {
    if (warnings?.length) setNotice(`SVG: пропущено — ${warnings.join(', ')}`)
  }

  const addImage = (file: File) =>
    api
      .uploadAsset(id, file)
      .then(({ asset, width, height, kind, warnings }) => {
        if (kind === 'pdf') throw new Error('Чтобы вставить страницы из PDF, используйте вкладку «Страницы»')
        const w = Math.min(200, width)
        addObject({ kind, w, h: (w * height) / width, padding: [0, 0, 0, 0], opacity: 1, asset })
        svgNotice(warnings)
      })
      .catch((e) => setError(e.message))

  const replaceImage = (pageIndex: number, b: Block, file: File) =>
    api
      .uploadAsset(id, file)
      .then(({ asset, kind, warnings }) => {
        svgNotice(warnings)
        if (kind === 'pdf') throw new Error('Нужна картинка или SVG')
        return b.object ? updateObject(pageIndex, { ...b.object, kind, asset }) : editOp(pageIndex, { op: 'replaceImage', blocks: [b.id], object: { asset } })
      })
      .catch((e) => setError(e.message))
  const undo = () => run(api.undo(id))
  const redo = () => run(api.redo(id))

  const select = (pageIndex: number, blockId: string | null, add: boolean) => {
    setSelected((cur) => {
      const key = blockId && `${pageIndex}:${blockId}`
      // Selection stays on one page: blocks of different pages cannot move together.
      const next = new Set(add ? [...cur].filter((k) => k.startsWith(`${pageIndex}:`)) : [])
      if (key) {
        if (add && cur.has(key)) next.delete(key)
        else next.add(key)
      }
      return next
    })
  }

  const selPage = selected.size ? Number([...selected][0].split(':')[0]) : -1
  const selBlocks = selPage >= 0 ? (blocksOf(selPage) ?? []).filter((b) => selected.has(`${selPage}:${b.id}`)) : []
  const pageLinks = selPage >= 0 ? (blocks.links(selPage) ?? []) : []
  const selLinks = pageLinks.filter((l) => selected.has(`${selPage}:${l.id}`))
  const selIds = selBlocks.map((b) => b.id)
  const saveLink = (pageIndex: number, link: Partial<Link>) => editOp(pageIndex, { op: 'link', link })

  // Arrow keys move by 1 pt (Shift: 10 pt), sent as one operation after a pause.
  const nudgeBy = (dxPx: number, dyPx: number) => {
    const n = nudge && nudge.page === selPage ? nudge : { page: selPage, ids: selIds, dx: 0, dy: 0 }
    const next = { ...n, dx: n.dx + dxPx, dy: n.dy + dyPx }
    setNudge(next)
    clearTimeout(nudgeTimer.current)
    nudgeTimer.current = setTimeout(() => {
      const [ux, uy] = deltaToUser(pages[next.page], scale, next.dx, next.dy)
      editOp(next.page, { op: 'transform', blocks: next.ids, matrix: translate(ux, uy) }).finally(() => setNudge(null))
    }, 400)
  }

  const removeSelected = () => {
    editOp(selPage, { op: 'delete', blocks: [...selIds, ...selLinks.map((l) => l.id)] })
    setSelected(new Set())
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest('input, textarea, [contenteditable]')) return
      const mod = e.ctrlKey || e.metaKey
      const arrows: Record<string, [number, number]> = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }
      if (e.key === 'Escape') setSelected(new Set())
      else if (mod && e.key.toLowerCase() === 'z') e.shiftKey ? redo() : undo()
      else if (mod && e.key.toLowerCase() === 'y') redo()
      else if ((e.key === 'Delete' || e.key === 'Backspace') && (selIds.length || selLinks.length) && editing) removeSelected()
      else if (arrows[e.key] && selIds.length && editing) {
        const d = (e.shiftKey ? 10 : 1) * scale
        nudgeBy(arrows[e.key][0] * d, arrows[e.key][1] * d)
      } else if (mod && (e.key === '=' || e.key === '+')) step(1)
      else if (mod && e.key === '-') step(-1)
      else if (mod && e.key === '0') setZoom(1)
      else return
      e.preventDefault()
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  })

  const problems = fonts.filter((f) => f.problem)
  const blocking = problems.filter((f) => f.blocking)

  return (
    <div class="editor">
      <header class="toolbar">
        <a class="btn ghost" href="#/">
          ← Документы
        </a>
        <span class="doc-name" title={info?.meta.name}>
          {info?.meta.name ?? '…'}
        </span>
        <span class="seg">
          <button class={`btn ${!editing ? 'active' : ''}`} onClick={() => setEditing(false)}>
            Просмотр
          </button>
          <button
            class={`btn ${editing ? 'active' : ''}`}
            disabled={readonly}
            title={readonly ? 'Документ защищён от изменений' : undefined}
            onClick={() => setEditing(true)}
          >
            Правка
          </button>
        </span>
        <span class="seg">
          <button class="btn" disabled={!state?.canUndo} onClick={undo} title="Отменить (Ctrl+Z)">
            Отменить
          </button>
          <button class="btn" disabled={!state?.canRedo} onClick={redo} title="Повторить (Ctrl+Shift+Z)">
            Повторить
          </button>
        </span>
        {editing && (
          <details class="menu">
            <summary class="btn">Добавить</summary>
            <div class="menu-list" onClick={(e) => (e.currentTarget.parentElement as HTMLDetailsElement).removeAttribute('open')}>
              <button
                onClick={() =>
                  addObject(
                    { kind: 'text', w: 200, h: 20, padding: [0, 0, 0, 0], opacity: 1, text: '', size: 12, color: '#000000', align: 'left', autoHeight: true },
                    true,
                  )
                }
              >
                Текст
              </button>
              <button
                onClick={() =>
                  addObject({
                    kind: 'table',
                    w: 360,
                    h: 60,
                    padding: [0, 0, 0, 0],
                    opacity: 1,
                    cols: [160, 100, 100],
                    rows: [{ cells: ['Наименование', 'Кол-во', 'Сумма'] }, { cells: ['', '', ''] }, { cells: ['', '', ''] }],
                    headerFill: '#e8eef7',
                    stroke: '#555555',
                    strokeWidth: 0.75,
                    cellPad: 4,
                    size: 10,
                    color: '#000000',
                    align: 'left',
                  })
                }
              >
                Таблица
              </button>
              <button onClick={() => addObject({ kind: 'rect', w: 120, h: 80, padding: [0, 0, 0, 0], opacity: 1, fill: '#e8eef7', stroke: '#555555', strokeWidth: 1 })}>
                Прямоугольник
              </button>
              <button onClick={() => addObject({ kind: 'ellipse', w: 100, h: 100, padding: [0, 0, 0, 0], opacity: 1, fill: '#d6f2e3', stroke: '#22aa77', strokeWidth: 1 })}>
                Эллипс
              </button>
              <button onClick={() => addObject({ kind: 'line', w: 200, h: 6, padding: [0, 0, 0, 0], opacity: 1, stroke: '#000000', strokeWidth: 1.5 })}>
                Линия
              </button>
              <label>
                Изображение…
                <input
                  type="file"
                  accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml,.svg"
                  hidden
                  onChange={(e) => {
                    const f = e.currentTarget.files?.[0]
                    if (f) addImage(f)
                    e.currentTarget.value = ''
                  }}
                />
              </label>
            </div>
          </details>
        )}
        <span class="spacer" />
        <span class="page-nav">
          <input
            class="num-input"
            value={pages.length ? page + 1 : ''}
            key={page}
            onKeyDown={(e) => e.key === 'Enter' && go(Number(e.currentTarget.value) - 1)}
          />
          <span class="muted">из {pages.length}</span>
        </span>
        <span class="zoom">
          <button class="btn" onClick={() => step(-1)} title="Уменьшить (Ctrl −)">
            −
          </button>
          <span class="zoom-value">{Math.round(scale * 100)}%</span>
          <button class="btn" onClick={() => step(1)} title="Увеличить (Ctrl +)">
            +
          </button>
          <button class={`btn ${zoom === 'fit' ? 'active' : ''}`} onClick={() => setZoom('fit')}>
            По ширине
          </button>
        </span>
        <span class="spacer" />
        <a class="btn" href={`${api.fileUrl(id, state?.rev)}&download`} title="Исходный файл + изменения (incremental update)">
          Скачать
        </a>
        <a class="btn ghost" href={`${api.fileUrl(id, state?.rev)}&download=clean`} title="Полная перезапись без старых ревизий">
          Чистая копия
        </a>
      </header>
      {error && <div class="banner error">{error}</div>}
      {form && editing && formHint && (
        <div class="banner info">
          В документе есть форма ({form.length} полей): заполняйте её в режиме <a onClick={() => setEditing(false)}>«Просмотр»</a>.
          <button class="btn ghost small" onClick={() => setFormHint(false)}>
            ✕
          </button>
        </div>
      )}
      {readonly && (
        <div class="banner warn">
          Владелец запретил изменять этот документ: доступен только просмотр. <a onClick={() => setAsk('owner')}>Ввести пароль владельца</a>
        </div>
      )}
      {ask && (
        <PasswordDialog
          id={id}
          owner={ask === 'owner'}
          onDone={() => {
            setAsk(null)
            setError('')
            if (ask === 'owner') api.doc(id).then(setInfo)
            else setReloadKey((k) => k + 1)
          }}
          onCancel={() => (ask === 'owner' ? setAsk(null) : (location.hash = '#/'))}
        />
      )}
      {notice && (
        <div class="banner info">
          {notice}
          <button class="btn ghost small" onClick={() => setNotice('')}>
            ✕
          </button>
        </div>
      )}
      {problems.length > 0 && !bannerHidden && (
        <div class={`banner ${blocking.length ? 'warn' : 'info'}`}>
          {blocking.length > 0
            ? `Шрифтов, которых нет в системе или которые не дают извлечь текст: ${blocking.length}. Вид или правка текста могут пострадать.`
            : `Шрифтов с ограничениями: ${problems.length}, для всех найдена замена в библиотеке.`}{' '}
          <a onClick={() => setTab('fonts')}>Подробнее</a>
          <button class="btn ghost small" onClick={() => setBannerHidden(true)}>
            ✕
          </button>
        </div>
      )}
      <div class="workspace">
        <aside class="sidebar">
          <nav class="tabs">
            {(
              [
                ['pages', 'Страницы'],
                ['outline', 'Закладки'],
                ['layout', 'Макет'],
                ['fonts', 'Шрифты'],
                ['doc', 'Документ'],
                ['objects', 'Объекты'],
              ] as [Tab, string][]
            ).map(([t, label]) => (
              <button key={t} class={tab === t ? 'active' : ''} onClick={() => setTab(t)}>
                {label}
                {t === 'fonts' && problems.length > 0 && <span class="count">{problems.length}</span>}
              </button>
            ))}
          </nav>
          <div class="tab-body">
            {pdf && tab === 'pages' && <Thumbs pdf={pdf} pages={pages} current={page} onNav={go} actions={editing ? pageActions : undefined} />}
            {tab === 'layout' && <LayoutPanel id={id} rev={state?.rev} pages={pages} current={page} guides={guides} setGuides={setGuides} run={run} />}
            {tab === 'outline' && (
              <OutlineEditor
                id={id}
                rev={state?.rev}
                pages={pages}
                current={{ page, top: pages[page] ? fromView(pages[page], scale, 0, pageOffset)[1] : 0 }}
                onNav={go}
                run={run}
              />
            )}
            {tab === 'fonts' && <FontsPanel fonts={fonts} progress={progress} />}
            {tab === 'doc' && <DocPanel id={id} rev={state?.rev} run={run} />}
            {tab === 'objects' && <Inspector id={id} pageNum={pages[page]?.num} />}
          </div>
        </aside>
        {pdf && info ? (
          <Viewer
            pdf={pdf}
            pages={pages}
            scale={scale}
            version={0}
            nav={nav}
            onPage={(i, off) => {
              setPage(i)
              setPageOffset(off)
            }}
            onWidth={setWidth}
            onZoomWheel={step}
            overlay={(i, s) => (
              <PageOverlay
                page={pages[i]}
                index={i}
                scale={s}
                blocks={editing ? blocksOf(i) : undefined}
                links={blocks.links(i)}
                onFollow={follow}
                guides={guides}
                selected={selected}
                editing={editing}
                nudge={nudge && nudge.page === i ? [nudge.dx, nudge.dy] : null}
                onSelect={select}
                onTransform={(pi, ids, matrix) => editOp(pi, { op: 'transform', blocks: ids, matrix })}
                onEditText={editText}
                onUpdate={updateObject}
              />
            )}
            overlayHTML={(i, s) =>
              !editing && form ? (
                <FormLayer fields={form} index={i} page={pages[i]} scale={s} onChange={(field, value) => run(api.edit(id, { op: 'field', page: 0, field, value }))} />
              ) : (
                textEdit?.page === i &&
                (!textEdit.cell ? (
                <RichTextEditor
                  key={textEdit.blockId}
                  edit={textEdit}
                  page={pages[i]}
                  scale={s}
                  library={library}
                  onCommit={(runs) => commitRich(textEdit, runs)}
                  onCancel={() => setTextEdit(null)}
                />
              ) : (
                <TextEditor
                  key={`${textEdit.blockId}:${textEdit.cell ?? ''}`}
                  edit={textEdit}
                  page={pages[i]}
                  scale={s}
                  onCommit={(t) => commitText(textEdit, t)}
                  onCancel={() => setTextEdit(null)}
                  onTab={textEdit.cell ? (t, back) => tabCell(textEdit, t, back) : undefined}
                />
                ))
              )
            }
          />
        ) : (
          <div class="viewer muted pad">Загружаю документ…</div>
        )}
        {editing && selLinks.length === 1 && selBlocks.length === 0 ? (
          <LinkProps
            key={selLinks[0].id}
            link={selLinks[0]}
            pages={pages}
            onSave={(l) => saveLink(selPage, { ...selLinks[0], ...l })}
            onDelete={removeSelected}
            onFollow={() => follow(selLinks[0])}
          />
        ) : editing && (
          <Properties
            blocks={selBlocks}
            page={pages[selPage]}
            fonts={fonts}
            onRecolor={(color) => editOp(selPage, { op: 'recolor', blocks: selIds, color })}
            onDelete={removeSelected}
            onRotate={(deg) => {
              const [x0, y0, x1, y1] = blocksBox(selBlocks)
              editOp(selPage, { op: 'transform', blocks: selIds, matrix: rotateAround(deg, (x0 + x1) / 2, (y0 + y1) / 2) })
            }}
            onMove={(dxMM, dyMM) => editOp(selPage, { op: 'transform', blocks: selIds, matrix: translate(dxMM * MM, dyMM * MM) })}
            library={library}
            onUpdate={(o) => updateObject(selPage, o)}
            onEditText={() => selBlocks[0] && editText(selPage, selBlocks[0])}
            onReplaceImage={(f) => selBlocks[0] && replaceImage(selPage, selBlocks[0], f)}
            linkSection={
              selBlocks.length === 1 && (
                <AttachLink
                  key={selBlocks[0].id}
                  link={pageLinks.find((l) => l.attach === selBlocks[0].id)}
                  pages={pages}
                  onSave={(l) => {
                    const old = pageLinks.find((x) => x.attach === selBlocks[0].id)
                    saveLink(selPage, { ...(old ?? {}), ...l, attach: selBlocks[0].id })
                  }}
                  onDelete={() => {
                    const old = pageLinks.find((x) => x.attach === selBlocks[0].id)
                    if (old) editOp(selPage, { op: 'delete', blocks: [old.id] })
                  }}
                />
              )
            }
          />
        )}
      </div>
    </div>
  )
}
