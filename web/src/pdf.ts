// The legacy build: the modern one needs 2025–2026 browser features
// (Math.sumPrecise, Map.getOrInsertComputed, URL.parse); this one brings
// them along and runs in Safari 17.4+, Chrome 119+, Firefox 121+.
import * as pdfjs from 'pdfjs-dist/legacy/build/pdf.mjs'
import workerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'

pdfjs.GlobalWorkerOptions.workerSrc = workerUrl

// In PDF a degenerate subpath (a zero-length line, a closed single point)
// stroked with round caps paints a dot (§8.5.3.2; charts draw their nodes
// so), but browsers following the canvas spec (Chrome 154+) drop zero-length
// segments before stroking. pdf.js builds paths as Path2D: a subpath that is
// only a point gets a segment of negligible length.
// ponytail: the line cap is not known here, so butt and square caps get the
// dot too (butt: invisible sliver; square: a small square, where the spec
// draws nothing).
{
  const P = Path2D.prototype
  const { moveTo, lineTo, bezierCurveTo, quadraticCurveTo, closePath } = P
  const dot = new WeakMap<Path2D, [number, number]>() // subpath so far is this point
  P.moveTo = function (this: Path2D, x: number, y: number) {
    dot.set(this, [x, y])
    moveTo.call(this, x, y)
  }
  P.lineTo = function (this: Path2D, x: number, y: number) {
    const d = dot.get(this)
    if (d && d[0] === x && d[1] === y) return lineTo.call(this, x + 1e-3, y)
    dot.delete(this)
    lineTo.call(this, x, y)
  }
  P.bezierCurveTo = function (this: Path2D, ...a: [number, number, number, number, number, number]) {
    dot.delete(this)
    bezierCurveTo.apply(this, a)
  }
  P.quadraticCurveTo = function (this: Path2D, ...a: [number, number, number, number]) {
    dot.delete(this)
    quadraticCurveTo.apply(this, a)
  }
  P.closePath = function (this: Path2D) {
    const d = dot.get(this)
    // Left open: a closed subpath gets joins, not caps.
    if (d) return lineTo.call(this, d[0] + 1e-3, d[1])
    closePath.call(this)
  }
}

export type { PDFDocumentProxy, PDFPageProxy } from 'pdfjs-dist/legacy/build/pdf.mjs'

// Range requests: for a 500-page file only the pages on screen are fetched.
// Returns the loading task: destroy() on it frees the worker-side document.
export function loadPdf(url: string, password?: string) {
  // Copied next to the app by the build (vite.config.ts); served from
  // node_modules by the dev server.
  const data = import.meta.env.DEV ? '/node_modules/pdfjs-dist/' : '/pdfjs/'
  return pdfjs.getDocument({
    url,
    password,
    disableAutoFetch: true,
    disableStream: true,
    rangeChunkSize: 65536,
    cMapUrl: data + 'cmaps/',
    standardFontDataUrl: data + 'standard_fonts/',
    wasmUrl: data + 'wasm/',
    iccUrl: data + 'iccs/',
  })
}
