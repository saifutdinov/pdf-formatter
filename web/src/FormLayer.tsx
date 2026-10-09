import { toView, type FormField, type PageInfo } from './api'

/**
 * Form fields of one page as live inputs over their widgets. A value goes
 * to the server when it is changed (text: on leaving the field or Enter).
 */
export function FormLayer({
  fields,
  index,
  page,
  scale,
  onChange,
}: {
  fields: FormField[]
  index: number
  page: PageInfo
  scale: number
  onChange: (name: string, value: string) => void
}) {
  return (
    <>
      {fields.flatMap((f) =>
        f.widgets
          .map((w, k) => [w, k] as const)
          .filter(([w]) => w.page === index)
          .map(([w, k]) => {
            const [ax, ay] = toView(page, scale, w.rect[0], w.rect[1])
            const [bx, by] = toView(page, scale, w.rect[2], w.rect[3])
            const box = {
              left: `${Math.min(ax, bx)}px`,
              top: `${Math.min(ay, by)}px`,
              width: `${Math.abs(bx - ax)}px`,
              height: `${Math.abs(by - ay)}px`,
            }
            const h = Math.abs(by - ay)
            const font = { fontSize: `${(f.fontSize || Math.min(12, (h / scale) * 0.6)) * scale}px` }
            const key = `${f.name}#${k}`
            const commit = (v: string) => v !== f.value && onChange(f.name, v)
            const align = (['left', 'center', 'right'] as const)[f.align ?? 0]
            switch (f.type) {
              case 'text':
                return f.multiline ? (
                  <textarea
                    key={key + f.value}
                    class="form-field"
                    style={{ ...box, ...font, textAlign: align }}
                    defaultValue={f.value}
                    disabled={f.readOnly}
                    maxLength={f.maxLen || undefined}
                    title={f.name}
                    onBlur={(e) => commit(e.currentTarget.value)}
                  />
                ) : (
                  <input
                    key={key + f.value}
                    class="form-field"
                    style={{ ...box, ...font, textAlign: align }}
                    defaultValue={f.value}
                    disabled={f.readOnly}
                    maxLength={f.maxLen || undefined}
                    title={f.name}
                    onBlur={(e) => commit(e.currentTarget.value)}
                    onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
                  />
                )
              case 'checkbox':
              case 'radio':
                return (
                  <input
                    key={key}
                    type={f.type}
                    class="form-field check"
                    style={box}
                    name={f.type === 'radio' ? `radio:${f.name}` : undefined}
                    checked={f.value === w.on}
                    disabled={f.readOnly || !w.on}
                    title={f.name}
                    onChange={(e) => commit(e.currentTarget.checked ? w.on! : 'Off')}
                  />
                )
              case 'choice':
                return (
                  <select key={key} class="form-field" style={{ ...box, ...font }} value={f.value} disabled={f.readOnly} title={f.name} onChange={(e) => commit(e.currentTarget.value)}>
                    {!f.options?.some((o) => o.value === f.value) && <option value={f.value}>{f.value}</option>}
                    {f.options?.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                )
            }
            return null
          }),
      )}
    </>
  )
}
