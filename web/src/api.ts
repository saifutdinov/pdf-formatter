export type Meta = {
  id: string
  name: string
  pages: number
  size: number
  created: string
  modified: string
  password?: boolean // encrypted with a user password: unlock first
}

/** A part of a text in its own style; empty fields take the object's. */
export type Run = { text: string; font?: string; size?: number; color?: string; underline?: boolean }

/** A form field and the places it is shown on the current pages. */
export type FormField = {
  name: string
  type: 'text' | 'checkbox' | 'radio' | 'choice' | 'button' | 'signature'
  value: string
  options?: { value: string; label: string }[]
  multiline?: boolean
  combo?: boolean
  readOnly?: boolean
  maxLen?: number
  align?: number
  fontSize?: number
  widgets: { page: number; rect: [number, number, number, number]; on?: string }[]
}

/** A step of the edit log, for the history panel. */
export type HistoryEntry = { op: string; at?: string; page: number; kind?: string; text?: string; new?: boolean }

/** Protection of an encrypted document. */
export type Security = { method: string; owner: boolean; password: boolean; editable: boolean }

export type PageInfo = {
  num: number // page id: object number, negative for inserted pages
  box: [number, number, number, number] // visible area in PDF units
  rotate: number
  label: string
}

export type Label = { start: number; style: '' | 'D' | 'R' | 'r' | 'A' | 'a'; prefix: string; first: number }

export type HFItem = {
  text: string
  position: 'top-left' | 'top-center' | 'top-right' | 'bottom-left' | 'bottom-center' | 'bottom-right'
  font?: string
  size: number
  color?: string
  offset: number
  side: number
  from: number
  to: number
  parity: 'all' | 'odd' | 'even'
  skipFirst: boolean
}

export type EditState = { rev: number; canUndo: boolean; canRedo: boolean; pages?: number[]; created?: string; structure?: boolean }

export type DocInfo = { meta: Meta; pages: PageInfo[]; state: EditState; security?: Security | null }

export type Link = {
  id: string
  kind: 'uri' | 'page' | 'other'
  quad: number[]
  uri?: string
  page?: number // target page object number
  top?: number
  attach?: string
}

export type OutlineItem = { title: string; page: number; top?: number; open?: boolean; items?: OutlineItem[] }

export type DocMeta = {
  title: string
  author: string
  subject: string
  keywords: string
  creator: string
  producer: string
  creationDate: string
  modDate: string
  lang: string
  displayDocTitle: boolean
  custom: Record<string, string>
}

export type Sanitize = { pieceInfo: boolean; thumbnails: boolean; javascript: boolean; embedded: boolean }

export type EditOp = {
  op:
    | 'transform'
    | 'recolor'
    | 'delete'
    | 'add'
    | 'update'
    | 'adopt'
    | 'replaceImage'
    | 'meta'
    | 'sanitize'
    | 'security'
    | 'field'
    | 'flatten'
    | 'table.split'
    | 'link'
    | 'outline'
    | 'page.insert'
    | 'page.delete'
    | 'page.move'
    | 'page.rotate'
    | 'page.margins'
    | 'labels'
    | 'hf'
    | 'toc'
    | 'page.import'
  page: number
  blocks?: string[]
  matrix?: number[]
  color?: string
  object?: Partial<EdObject>
  meta?: DocMeta
  sanitize?: Sanitize
  link?: Partial<Link>
  outline?: OutlineItem[]
  decrypt?: boolean // security: the clean copy drops the encryption
  field?: string // field: fully qualified name
  value?: string // field: new value (a state name for buttons)
  after?: number
  size?: [number, number]
  rotate?: number
  pages?: number[]
  mode?: 'resize' | 'fit'
  margins?: [number, number, number, number]
  labels?: Label[]
  hf?: HFItem[]
  toc?: { title: string; maxLevel: number; font?: string; size?: number }
  asset?: string
  from?: number[]
}

export type Inspected = {
  ref: string
  type: string
  text: string
  decoded?: string
  length?: number
  error?: string
}

export type Block = {
  id: string
  kind: 'text' | 'image' | 'path' | 'table'
  quad: number[] // 4 corners in PDF user space: bl, br, tr, tl
  angle?: number
  text?: string
  flow?: string // text for editing: soft line breaks joined, every character kept
  runs?: Run[] // the flow in several styles (library fonts)
  font?: string
  fontRef?: number
  size?: number
  color?: string
  bold?: boolean
  italic?: boolean
  invisible?: boolean
  artifact?: boolean
  inForm?: boolean
  libFont?: string // library face used when the text is edited
  object?: EdObject // editor objects only
  rowHeights?: number[] // tables
}

/** An object owned by the editor. Local frame: origin top-left, y up, box [0,w]×[-h,0]. */
export type EdObject = {
  id: string
  kind: 'text' | 'rect' | 'ellipse' | 'line' | 'image' | 'svg' | 'table'
  matrix: number[]
  w: number
  h: number
  padding: number[] // top, right, bottom, left (pt)
  fill?: string
  stroke?: string
  strokeWidth?: number
  opacity: number
  text?: string
  runs?: Run[] // parts of the text in their own style; text is their concatenation
  font?: string
  size?: number
  color?: string
  align?: 'left' | 'center' | 'right' | 'justify'
  valign?: 'top' | 'middle' | 'bottom'
  lineHeight?: number
  autoHeight?: boolean
  asset?: string
  cols?: number[]
  rows?: { cells: string[]; height?: number }[]
  headerFill?: string
  cellPad?: number
  spans?: Span[] // merged cells
}

/** Cell rectangle of a table in its local frame: x, top y (≤ 0), width, height. */
/** Merged table cells: rows × cols from (row, col); the first holds the text. */
export type Span = { row: number; col: number; rows: number; cols: number }

/** The cells a cell belongs to: its merge, or itself. */
export function spanAt(o: EdObject, r: number, c: number): Span {
  return o.spans?.find((s) => r >= s.row && r < s.row + s.rows && c >= s.col && c < s.col + s.cols) ?? { row: r, col: c, rows: 1, cols: 1 }
}

/** Box of the cell (merged cells as one) in the table frame: x, top y, w, h. */
export function cellRect(o: EdObject, rowHeights: number[], r: number, c: number): [number, number, number, number] {
  const s = spanAt(o, r, c)
  const cols = o.cols ?? []
  const sum = (a: number[]) => a.reduce((x, y) => x + y, 0)
  return [sum(cols.slice(0, s.col)), -sum(rowHeights.slice(0, s.row)), sum(cols.slice(s.col, s.col + s.cols)), sum(rowHeights.slice(s.row, s.row + s.rows))]
}

/**
 * Merges the cells of a rectangle, grown to take in every merge it touches;
 * the texts join into the first cell, line by line.
 */
export function mergeCells(o: EdObject, r0: number, c0: number, r1: number, c1: number): EdObject {
  const spans = o.spans ?? []
  for (let grown = true; grown; ) {
    grown = false
    for (const s of spans) {
      if (s.row <= r1 && s.row + s.rows - 1 >= r0 && s.col <= c1 && s.col + s.cols - 1 >= c0) {
        const n = [Math.min(r0, s.row), Math.min(c0, s.col), Math.max(r1, s.row + s.rows - 1), Math.max(c1, s.col + s.cols - 1)]
        if (n[0] !== r0 || n[1] !== c0 || n[2] !== r1 || n[3] !== c1) [r0, c0, r1, c1, grown] = [...n, true] as [number, number, number, number, boolean]
      }
    }
  }
  const rows = (o.rows ?? []).map((row) => ({ ...row, cells: [...row.cells] }))
  const texts: string[] = []
  for (let r = r0; r <= r1; r++)
    for (let c = c0; c <= c1; c++) {
      const t = rows[r]?.cells[c]?.trim()
      if (t) texts.push(t)
      if (rows[r] && (r !== r0 || c !== c0)) rows[r].cells[c] = ''
    }
  if (rows[r0]) rows[r0].cells[c0] = texts.join('\n')
  const kept = spans.filter((s) => s.row > r1 || s.row + s.rows - 1 < r0 || s.col > c1 || s.col + s.cols - 1 < c0)
  return { ...o, rows, spans: [...kept, { row: r0, col: c0, rows: r1 - r0 + 1, cols: c1 - c0 + 1 }] }
}

/** Undoes the merge a cell belongs to. */
export function splitCells(o: EdObject, r: number, c: number): EdObject {
  const s = spanAt(o, r, c)
  return { ...o, spans: (o.spans ?? []).filter((x) => x.row !== s.row || x.col !== s.col) }
}

export type FontInfo = {
  refs: number[]
  name: string
  baseFont: string
  subtype: string
  status: 'embedded' | 'subset' | 'missing' | 'standard'
  hasText: boolean
  pages: number[] | null
  chars: string
  problem?: string
  match?: LibFont
  blocking: boolean
}

export type LibFont = {
  id: string
  psName: string
  family: string
  style: string
  full: string
  bold: boolean
  italic: boolean
  cyrillic: boolean
  format: 'ttf' | 'otf'
  source: 'builtin' | 'licensed' | 'system' | 'upload'
}

export type Progress = { done: number; total: number; finished: boolean }

// Changes go to the server one at a time, in the order they were made.
let chain: Promise<unknown> = Promise.resolve()
function serial<T>(f: () => Promise<T>): Promise<T> {
  const p = chain.then(f, f)
  chain = p.catch(() => {})
  return p
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
  }
}

async function call<T>(url: string, init?: RequestInit): Promise<T> {
  const r = await fetch(url, init)
  const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }))
  if (!r.ok) throw new ApiError(j.error ?? `HTTP ${r.status}`, r.status)
  return j as T
}

/** Passwords entered in this tab, for pdf.js: never stored anywhere else. */
export const passwords = new Map<string, string>()

export const api = {
  list: () => call<Meta[]>('/api/docs'),
  doc: (id: string) => call<DocInfo>(`/api/docs/${id}`),
  fields: (id: string) => call<{ fields: FormField[]; flattened: boolean }>(`/api/docs/${id}/fields`),
  unlock: (id: string, password: string) =>
    call<{ ok: boolean }>(`/api/docs/${id}/unlock`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password }) }),
  remove: (id: string) => call<{ ok: boolean }>(`/api/docs/${id}`, { method: 'DELETE' }),
  upload: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return call<Meta>('/api/docs', { method: 'POST', body: fd })
  },
  object: (id: string, num: number | 'trailer') => call<Inspected>(`/api/docs/${id}/objects/${num}`),
  fileUrl: (id: string, version = 0) => `/api/docs/${id}/file?v=${version}`,
  blocks: (id: string, page: number) => call<{ blocks: Block[]; links: Link[] }>(`/api/docs/${id}/pages/${page}/blocks`),
  layout: (id: string) => call<{ labels: Label[] | null; hf: HFItem[] | null }>(`/api/docs/${id}/layout`),
  outline: (id: string, suggest = false) => call<{ outline: OutlineItem[] | null }>(`/api/docs/${id}/outline${suggest ? '?suggest' : ''}`),
  fonts: (id: string) => call<{ fonts: FontInfo[]; progress: Progress }>(`/api/docs/${id}/fonts`),
  edit: (id: string, op: EditOp) =>
    serial(() => call<EditState>(`/api/docs/${id}/ops`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(op) })),
  meta: (id: string) => call<{ meta: DocMeta; sanitize: Sanitize; security: Security | null; decrypt: boolean }>(`/api/docs/${id}/meta`),
  uploadAsset: (id: string, file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return call<{ asset: string; width: number; height: number; kind: 'image' | 'svg' | 'pdf'; pages?: number; warnings?: string[] }>(`/api/docs/${id}/assets`, {
      method: 'POST',
      body: fd,
    })
  },
  undo: (id: string) => serial(() => call<EditState>(`/api/docs/${id}/undo`, { method: 'POST' })),
  history: (id: string) => call<{ done: HistoryEntry[]; undone: HistoryEntry[] }>(`/api/docs/${id}/history`),
  goto: (id: string, n: number) =>
    serial(() => call<EditState>(`/api/docs/${id}/goto`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ n }) })),
  redo: (id: string) => serial(() => call<EditState>(`/api/docs/${id}/redo`, { method: 'POST' })),
  library: () => call<{ fonts: LibFont[]; default: string; ready: boolean }>('/api/fonts'),
  uploadFonts: (files: FileList | File[]) => {
    const fd = new FormData()
    for (const f of Array.from(files)) fd.append('file', f)
    return call<{ added: LibFont[] }>('/api/fonts', { method: 'POST', body: fd })
  },
  removeFont: (id: string) => call<{ ok: boolean }>(`/api/fonts/${id}`, { method: 'DELETE' }),
  setDefaultFont: (id: string) =>
    call<{ ok: boolean }>('/api/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ defaultFont: id }) }),
}

/** Size of a page on screen at scale 1, in CSS pixels (= PDF points). */
export function pageSize(p: PageInfo): [number, number] {
  const w = p.box[2] - p.box[0]
  const h = p.box[3] - p.box[1]
  return p.rotate % 180 ? [h, w] : [w, h]
}

/** Maps a point in PDF user space to CSS pixels on the rendered page. */
export function toView(p: PageInfo, scale: number, x: number, y: number): [number, number] {
  const [x0, y0, x1, y1] = p.box
  switch (p.rotate) {
    case 90:
      return [(y - y0) * scale, (x - x0) * scale]
    case 180:
      return [(x1 - x) * scale, (y - y0) * scale]
    case 270:
      return [(y1 - y) * scale, (x1 - x) * scale]
  }
  return [(x - x0) * scale, (y1 - y) * scale]
}

export const MM = 72 / 25.4
export const fmtMM = (pt: number) => (pt / MM).toFixed(1).replace('.', ',')

/** Inverse of toView: CSS pixels on the page → PDF user space. */
export function fromView(p: PageInfo, scale: number, X: number, Y: number): [number, number] {
  const [x0, y0, x1, y1] = p.box
  const u = X / scale
  const v = Y / scale
  switch (p.rotate) {
    case 90:
      return [v + x0, u + y0]
    case 180:
      return [x1 - u, v + y0]
    case 270:
      return [x1 - v, y1 - u]
  }
  return [u + x0, y1 - v]
}

/** A screen-space displacement in PDF user space units. */
export function deltaToUser(p: PageInfo, scale: number, dx: number, dy: number): [number, number] {
  const [ax, ay] = fromView(p, scale, 0, 0)
  const [bx, by] = fromView(p, scale, dx, dy)
  return [bx - ax, by - ay]
}

export const translate = (dx: number, dy: number) => [1, 0, 0, 1, dx, dy]

/** Rotation by deg (counter-clockwise in user space) around (cx, cy). */
export function rotateAround(deg: number, cx: number, cy: number) {
  const r = (deg * Math.PI) / 180
  const c = Math.cos(r)
  const s = Math.sin(r)
  return [c, s, -s, c, cx - c * cx + s * cy, cy - s * cx - c * cy]
}

/** Bounding box [x0, y0, x1, y1] of block outlines in user space. */
export function blocksBox(blocks: Block[]): [number, number, number, number] {
  let b: [number, number, number, number] = [Infinity, Infinity, -Infinity, -Infinity]
  for (const bl of blocks) {
    for (let k = 0; k < 8; k += 2) {
      b = [Math.min(b[0], bl.quad[k]), Math.min(b[1], bl.quad[k + 1]), Math.max(b[2], bl.quad[k]), Math.max(b[3], bl.quad[k + 1])]
    }
  }
  return b
}

/** Applies a PDF matrix to a point. */
export const apply = (m: number[], x: number, y: number): [number, number] => [x * m[0] + y * m[2] + m[4], x * m[1] + y * m[3] + m[5]]

/** Inverse of a PDF matrix. */
export function invert(m: number[]): number[] {
  const det = m[0] * m[3] - m[1] * m[2] || 1e-12
  return [m[3] / det, -m[1] / det, -m[2] / det, m[0] / det, (m[2] * m[5] - m[3] * m[4]) / det, (m[1] * m[4] - m[0] * m[5]) / det]
}

/** Outline of an object (bl, br, tr, tl) in user space. */
export function objectQuad(o: Pick<EdObject, 'matrix' | 'w' | 'h'>): number[] {
  const out: number[] = []
  for (const [x, y] of [
    [0, -o.h],
    [o.w, -o.h],
    [o.w, 0],
    [0, 0],
  ])
    out.push(...apply(o.matrix, x, y))
  return out
}
