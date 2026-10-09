import { blocksBox, fmtMM, MM, type Block, type EdObject, type FontInfo, type LibFont, type PageInfo } from './api'
import { FontBadge, fontFor } from './Panels'

const KIND: Record<string, string> = {
  text: 'Текст',
  image: 'Изображение',
  path: 'Графика',
  rect: 'Прямоугольник',
  ellipse: 'Эллипс',
  line: 'Линия',
  table: 'Таблица',
  svg: 'Векторный рисунок (SVG)',
}

type Props = {
  blocks: Block[]
  page?: PageInfo
  fonts: FontInfo[]
  library: LibFont[]
  onRecolor: (color: string) => void
  onDelete: () => void
  onRotate: (deg: number) => void
  onMove: (dxMM: number, dyMM: number) => void // user space: x right, y up
  onUpdate: (obj: EdObject) => void
  onEditText: () => void
  onReplaceImage: (file: File) => void
  linkSection?: preact.ComponentChildren
}

const parseNum = (s: string) => Number(s.replace(',', '.'))
const fmt = (v: number, digits = 1) => String(Math.round(v * 10 ** digits) / 10 ** digits).replace('.', ',')

/** A number field that commits on Enter or blur. */
function NumField({ value, suffix, onCommit, disabled }: { value: string; suffix?: string; onCommit: (v: number) => void; disabled?: boolean }) {
  return (
    <span class="num-field">
      <input
        key={value}
        defaultValue={value}
        disabled={disabled}
        onKeyDown={(e) => {
          if (e.key === 'Enter') e.currentTarget.blur()
        }}
        onBlur={(e) => {
          const raw = e.currentTarget.value
          const v = parseNum(raw)
          if (raw !== value && Number.isFinite(v)) onCommit(v)
        }}
      />
      {suffix}
    </span>
  )
}

/** Colour with an optional "none". */
function ColorField({ value, onChange, optional }: { value?: string; onChange: (v: string) => void; optional?: boolean }) {
  return (
    <span class="color-field">
      <input type="color" class="color-input" value={value || '#000000'} key={value} onChange={(e) => onChange(e.currentTarget.value)} />
      {optional && (
        <label class="small">
          <input type="checkbox" checked={!value} onChange={(e) => onChange(e.currentTarget.checked ? '' : '#000000')} /> нет
        </label>
      )}
    </span>
  )
}

function Seg<T extends string>({ value, options, onChange }: { value: T; options: [T, string][]; onChange: (v: T) => void }) {
  return (
    <span class="seg small-seg">
      {options.map(([v, label]) => (
        <button key={v} class={`btn small ${value === v ? 'active' : ''}`} onClick={() => onChange(v)}>
          {label}
        </button>
      ))}
    </span>
  )
}

function TableSection({ o, set }: { o: EdObject; set: (patch: Partial<EdObject>) => void }) {
  const cols = o.cols ?? []
  const rows = o.rows ?? []
  const width = cols.reduce((a, b) => a + b, 0)
  return (
    <div class="props-section">
      <div class="hf-row">
        <span>Строк: {rows.length}</span>
        <button class="btn small" onClick={() => set({ rows: [...rows, { cells: cols.map(() => '') }] })}>
          +
        </button>
        <button class="btn small" disabled={rows.length < 2} onClick={() => set({ rows: rows.slice(0, -1) })}>
          −
        </button>
        <span>Столбцов: {cols.length}</span>
        <button
          class="btn small"
          onClick={() => {
            // A new column takes its width from the others, the table keeps its width.
            const w = width / (cols.length + 1)
            const k = (width - w) / width
            set({ cols: [...cols.map((c) => c * k), w], rows: rows.map((r) => ({ ...r, cells: [...r.cells, ''] })) })
          }}
        >
          +
        </button>
        <button
          class="btn small"
          disabled={cols.length < 2}
          onClick={() => {
            const last = cols[cols.length - 1]
            const k = width / (width - last)
            set({ cols: cols.slice(0, -1).map((c) => c * k), rows: rows.map((r) => ({ ...r, cells: r.cells.slice(0, -1) })) })
          }}
        >
          −
        </button>
      </div>
      <div class="hf-row">
        <span>Ширины, мм:</span>
        {cols.map((c, k) => (
          <span class="num-field" key={k}>
            <input
              key={c}
              defaultValue={fmtMM(c)}
              onBlur={(e) => {
                const v = Number(e.currentTarget.value.replace(',', '.'))
                if (Number.isFinite(v) && v > 2) set({ cols: cols.map((x, j) => (j === k ? v * MM : x)) })
              }}
            />
          </span>
        ))}
      </div>
      <div class="hf-row">
        <label class="small">
          <input type="checkbox" checked={!!o.headerFill} onChange={(e) => set({ headerFill: e.currentTarget.checked ? '#e8eef7' : '' })} /> строка-заголовок
        </label>
        {o.headerFill && <ColorField value={o.headerFill} onChange={(headerFill) => set({ headerFill })} />}
      </div>
      <div class="hf-row">
        <span>Поля ячеек</span>
        <NumField value={fmtMM(o.cellPad ?? 4)} suffix="мм" onCommit={(v) => set({ cellPad: Math.max(0, v * MM) })} />
      </div>
      <p class="muted small">Двойной клик по ячейке — правка, Tab — следующая ячейка. Границы столбцов можно тянуть мышью.</p>
    </div>
  )
}

function ReplaceImage({ onFile }: { onFile: (f: File) => void }) {
  return (
    <label class="btn">
      Заменить изображение
      <input
        type="file"
        accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml,.svg"
        hidden
        onChange={(e) => {
          const f = e.currentTarget.files?.[0]
          if (f) onFile(f)
          e.currentTarget.value = ''
        }}
      />
    </label>
  )
}

export function Properties(p: Props) {
  const { blocks, page, fonts } = p
  if (!blocks.length || !page) {
    return (
      <aside class="props">
        <h3>Свойства</h3>
        <p class="muted">
          Выберите блок на странице. Перетаскивайте мышью, стрелки двигают на 1 pt (Shift — на 10), Delete удаляет. Двойной клик по тексту —
          правка. Shift+клик выделяет несколько блоков, Esc снимает выделение.
        </p>
      </aside>
    )
  }
  const b = blocks[0]
  const single = blocks.length === 1
  const [x0, y0, x1, y1] = blocksBox(blocks)
  const left = (x0 - page.box[0]) / MM
  const top = (page.box[3] - y1) / MM
  const angle = single ? (b.angle ?? 0) : 0
  const position = (
    <>
      <dt>X</dt>
      <dd>
        <NumField value={fmtMM(x0 - page.box[0])} suffix="мм" onCommit={(v) => p.onMove(v - left, 0)} />
      </dd>
      <dt>Y</dt>
      <dd>
        <NumField value={fmtMM(page.box[3] - y1)} suffix="мм" onCommit={(v) => p.onMove(0, top - v)} />
      </dd>
      <dt>Поворот</dt>
      <dd>
        <NumField value={fmt(angle)} suffix="°" onCommit={(v) => p.onRotate(v - angle)} />
      </dd>
    </>
  )

  if (single && b.object) return <ObjectProps {...p} obj={b.object} position={position} />

  const f = single ? fontFor(fonts, b.fontRef) : undefined
  const lib = single && b.libFont ? p.library.find((l) => l.id === b.libFont) : undefined
  const colorable = blocks.some((bl) => bl.kind !== 'image')
  return (
    <aside class="props">
      <h3>{single ? KIND[b.kind] : `Выбрано блоков: ${blocks.length}`}</h3>
      {single && b.text !== undefined && <div class="props-text">{b.text}</div>}
      {single && b.kind === 'text' && (
        <button class="btn primary wide" onClick={p.onEditText}>
          Редактировать текст
        </button>
      )}
      {single && b.kind === 'image' && <ReplaceImage onFile={p.onReplaceImage} />}
      <dl>
        {position}
        <dt>Размер</dt>
        <dd>
          {fmtMM(x1 - x0)} × {fmtMM(y1 - y0)} мм
        </dd>
        {single && b.font && (
          <>
            <dt>Шрифт</dt>
            <dd>
              {b.font} {f && <FontBadge f={f} />}
            </dd>
            <dt>Кегль</dt>
            <dd>{b.size} pt</dd>
          </>
        )}
        {colorable && (
          <>
            <dt>Цвет</dt>
            <dd>
              <ColorField value={b.color} onChange={p.onRecolor} /> {single && b.color}
            </dd>
          </>
        )}
      </dl>
      {lib && <p class="muted small">При правке текст будет набран шрифтом «{lib.full}» из библиотеки.</p>}
      {p.linkSection && (
        <div class="props-section">
          <h4>Ссылка</h4>
          {p.linkSection}
        </div>
      )}
      <button class="btn danger" onClick={p.onDelete}>
        Удалить
      </button>
      {f?.problem && <div class="font-problem">{f.problem}</div>}
      {single && b.inForm && <p class="muted small">Внутри формы (XObject): при правке форма встроится в страницу.</p>}
      {single && b.artifact && <p class="muted small">Помечен как артефакт (колонтитул, фон).</p>}
      {single && b.invisible && <p class="muted small">Невидимый текст (например, слой распознавания поверх скана).</p>}
    </aside>
  )
}

function ObjectProps(p: Props & { obj: EdObject; position: preact.JSX.Element }) {
  const o = p.obj
  const set = (patch: Partial<EdObject>) => p.onUpdate({ ...o, ...patch })
  const pad = o.padding ?? [0, 0, 0, 0]
  const fontOptions = p.library.filter((f) => f.cyrillic || f.id === o.font)
  return (
    <aside class="props">
      <h3>{KIND[o.kind]}</h3>
      {o.kind === 'text' && (
        <button class="btn primary wide" onClick={p.onEditText}>
          Редактировать текст
        </button>
      )}
      {(o.kind === 'image' || o.kind === 'svg') && <ReplaceImage onFile={p.onReplaceImage} />}
      {o.kind === 'table' && <TableSection o={o} set={set} />}
      <dl>
        {p.position}
        {o.kind === 'table' ? null : (
          <>
        <dt>Ширина</dt>
        <dd>
          <NumField value={fmtMM(o.w)} suffix="мм" onCommit={(v) => set({ w: Math.max(1, v * MM) })} />
        </dd>
        <dt>Высота</dt>
        <dd>
          <NumField value={fmtMM(o.h)} suffix="мм" disabled={o.kind === 'text' && o.autoHeight} onCommit={(v) => set({ h: Math.max(1, v * MM) })} />
          {o.kind === 'text' && (
            <label class="small">
              <input type="checkbox" checked={!!o.autoHeight} onChange={(e) => set({ autoHeight: e.currentTarget.checked })} /> авто
            </label>
          )}
        </dd>
          </>
        )}
        {(o.kind === 'text' || o.kind === 'table') && (
          <>
            <dt>Шрифт</dt>
            <dd>
              <select class="select" value={o.font} onChange={(e) => set({ font: e.currentTarget.value })}>
                <option value="">По умолчанию</option>
                {fontOptions.map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.full}
                  </option>
                ))}
              </select>
            </dd>
            <dt>Кегль</dt>
            <dd>
              <NumField value={fmt(o.size ?? 12)} suffix="pt" onCommit={(v) => set({ size: Math.min(500, Math.max(1, v)) })} />
            </dd>
            <dt>Цвет</dt>
            <dd>
              <ColorField value={o.color} onChange={(color) => set({ color: color || '#000000' })} />
            </dd>
            <dt>Выравн.</dt>
            <dd>
              <Seg
                value={o.align ?? 'left'}
                options={[
                  ['left', 'Лево'],
                  ['center', 'Центр'],
                  ['right', 'Право'],
                  ['justify', 'Ширина'],
                ]}
                onChange={(align) => set({ align })}
              />
            </dd>
            <dt>Верт.</dt>
            <dd>
              <Seg
                value={o.valign ?? 'top'}
                options={[
                  ['top', 'Верх'],
                  ['middle', 'Центр'],
                  ['bottom', 'Низ'],
                ]}
                onChange={(valign) => set({ valign })}
              />
            </dd>
            <dt>Интервал</dt>
            <dd>
              <NumField value={fmt(o.lineHeight || 1.2, 2)} onCommit={(v) => set({ lineHeight: Math.min(5, Math.max(0.5, v)) })} />
            </dd>
            {o.kind === 'text' && <>
            <dt>Поля</dt>
            <dd class="pad-fields">
              {['↑', '→', '↓', '←'].map((label, k) => (
                <span key={k} title={['Сверху', 'Справа', 'Снизу', 'Слева'][k]}>
                  {label}
                  <NumField
                    value={fmtMM(pad[k])}
                    onCommit={(v) => {
                      const next = [...pad]
                      next[k] = Math.max(0, v * MM)
                      set({ padding: next })
                    }}
                  />
                </span>
              ))}
              <span class="muted small">мм</span>
            </dd>
            </>}
          </>
        )}
        {o.kind !== 'image' && o.kind !== 'svg' && o.kind !== 'line' && (
          <>
            <dt>{o.kind === 'text' || o.kind === 'table' ? 'Фон' : 'Заливка'}</dt>
            <dd>
              <ColorField value={o.fill} optional onChange={(fill) => set({ fill })} />
            </dd>
          </>
        )}
        {o.kind !== 'image' && o.kind !== 'svg' && (
          <>
            <dt>{o.kind === 'line' ? 'Линия' : 'Рамка'}</dt>
            <dd>
              <ColorField value={o.stroke} optional={o.kind !== 'line'} onChange={(stroke) => set({ stroke, strokeWidth: o.strokeWidth || 1 })} />
              <NumField value={fmt(o.strokeWidth ?? 0)} suffix="pt" onCommit={(v) => set({ strokeWidth: Math.max(0, v) })} />
            </dd>
          </>
        )}
        <dt>Прозрачн.</dt>
        <dd>
          <input
            type="range"
            min={10}
            max={100}
            value={Math.round((o.opacity || 1) * 100)}
            onChange={(e) => set({ opacity: Number(e.currentTarget.value) / 100 })}
          />{' '}
          {Math.round((o.opacity || 1) * 100)}%
        </dd>
      </dl>
      {p.linkSection && (
        <div class="props-section">
          <h4>Ссылка</h4>
          {p.linkSection}
        </div>
      )}
      <button class="btn danger" onClick={p.onDelete}>
        Удалить
      </button>
    </aside>
  )
}
