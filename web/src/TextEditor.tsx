import { useLayoutEffect, useRef } from 'preact/hooks'
import { toView, type EdObject, type PageInfo } from './api'
import { useLibFont } from './fontface'

export type TextEdit = {
  page: number
  blockId: string
  own: boolean // editor object (update) or original block (adopt)
  text: string
  font: string
  size: number
  color: string
  align: string
  lineHeight: number
  padding: number[]
  quad: number[]
  cell?: [number, number] // table cell being edited
  obj?: EdObject // the table as edited so far (ahead of the server)
}

/** A textarea laid over the block, styled like the result. */
export function TextEditor({
  edit,
  page,
  scale,
  onCommit,
  onCancel,
  onTab,
}: {
  edit: TextEdit
  page: PageInfo
  scale: number
  onCommit: (text: string) => void
  onCancel: () => void
  onTab?: (text: string, back: boolean) => void
}) {
  const ta = useRef<HTMLTextAreaElement>(null)
  const family = useLibFont(edit.font)
  const q = edit.quad
  const [blx, bly] = toView(page, scale, q[0], q[1])
  const [trx, try_] = toView(page, scale, q[4], q[5])
  const [tlx, tly] = toView(page, scale, q[6], q[7])
  const width = Math.hypot(trx - tlx, try_ - tly)
  const height = Math.hypot(blx - tlx, bly - tly)
  const angle = (Math.atan2(try_ - tly, trx - tlx) * 180) / Math.PI
  const grow = () => {
    const el = ta.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.max(height, el.scrollHeight)}px`
  }
  // Focus synchronously on mount: keys typed right after Tab must land here.
  useLayoutEffect(() => {
    const el = ta.current!
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
    grow()
    document.fonts.ready.then(grow)
  }, [])
  const done = useRef(false)
  const commit = () => {
    if (done.current) return
    done.current = true
    const v = ta.current!.value
    v !== edit.text ? onCommit(v) : onCancel()
  }
  const p = edit.padding.map((v) => v * scale)
  return (
    <textarea
      ref={ta}
      class="text-editor"
      defaultValue={edit.text}
      spellcheck
      style={{
        left: `${tlx}px`,
        top: `${tly}px`,
        width: `${Math.max(width, 40)}px`,
        transform: `rotate(${angle}deg)`,
        fontFamily: family,
        fontSize: `${edit.size * scale}px`,
        lineHeight: edit.lineHeight,
        color: edit.color,
        textAlign: edit.align === 'justify' ? 'justify' : (edit.align as 'left'),
        padding: `${p[0]}px ${p[1]}px ${p[2]}px ${p[3]}px`,
      }}
      onInput={grow}
      onBlur={commit}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === 'Escape') {
          done.current = true
          onCancel()
        } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) commit()
        else if (e.key === 'Tab' && onTab) {
          e.preventDefault()
          done.current = true
          onTab(ta.current!.value, e.shiftKey)
        }
      }}
    />
  )
}
