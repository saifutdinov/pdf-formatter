export type Meta = {
  id: string
  name: string
  pages: number
  size: number
  created: string
  modified: string
}

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

export type DocInfo = { meta: Meta; pages: PageInfo[]; state: EditState }

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
}

/** Cell rectangle of a table in its local frame: x, top y (≤ 0), width, height. */
export function cellRect(o: EdObject, rowHeights: number[], r: number, c: number): [number, number, number, number] {
  const cols = o.cols ?? []
  const x = cols.slice(0, c).reduce((a, b) => a + b, 0)
  const y = -rowHeights.slice(0, r).reduce((a, b) => a + b, 0)
  return [x, y, cols[c], rowHeights[r]]
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

async function call<T>(url: string, init?: RequestInit): Promise<T> {
  const r = await fetch(url, init)
  const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }))
  if (!r.ok) throw new Error(j.error ?? `HTTP ${r.status}`)
  return j as T
}

export const api = {
  list: () => call<Meta[]>('/api/docs'),
  doc: (id: string) => call<DocInfo>(`/api/docs/${id}`),
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
  meta: (id: string) => call<{ meta: DocMeta; sanitize: Sanitize }>(`/api/docs/${id}/meta`),
  uploadAsset: (id: string, file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return call<{ asset: string; width: number; height: number; kind: 'image' | 'svg' | 'pdf'; pages?: number; warnings?: string[] }>(`/api/docs/${id}/assets`, {
      method: 'POST',
      body: fd,
    })
  },
  undo: (id: string) => serial(() => call<EditState>(`/api/docs/${id}/undo`, { method: 'POST' })),
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
