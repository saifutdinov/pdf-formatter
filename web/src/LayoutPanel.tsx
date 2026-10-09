import { useEffect, useState } from 'preact/hooks'
import { api, MM, type EditState, type HFItem, type Label, type PageInfo } from './api'

type Props = {
  id: string
  rev?: number
  pages: PageInfo[]
  current: number
  guides: number[] | null // margins in pt for the dashed guides, or null when hidden
  setGuides: (g: number[] | null) => void
  run: (p: Promise<EditState>) => Promise<unknown>
}

const num = (s: string) => Number(s.replace(',', '.'))
const mm = (pt: number) => String(Math.round((pt / MM) * 10) / 10).replace('.', ',')

const POSITIONS: [HFItem['position'], string][] = [
  ['top-left', 'Сверху слева'],
  ['top-center', 'Сверху по центру'],
  ['top-right', 'Сверху справа'],
  ['bottom-left', 'Снизу слева'],
  ['bottom-center', 'Снизу по центру'],
  ['bottom-right', 'Снизу справа'],
]

const STYLES: [Label['style'], string][] = [
  ['D', '1, 2, 3'],
  ['r', 'i, ii, iii'],
  ['R', 'I, II, III'],
  ['a', 'a, b, c'],
  ['A', 'A, B, C'],
  ['', 'только префикс'],
]

function MarginInputs({ value, set }: { value: number[]; set: (v: number[]) => void }) {
  return (
    <div class="margin-grid">
      {['Сверху', 'Справа', 'Снизу', 'Слева'].map((label, k) => (
        <label key={k}>
          <span>{label}</span>
          <input
            value={mm(value[k])}
            onChange={(e) => {
              const v = num(e.currentTarget.value)
              if (Number.isFinite(v)) set(value.map((x, j) => (j === k ? v * MM : x)))
            }}
          />
        </label>
      ))}
      <span class="muted small">мм</span>
    </div>
  )
}

export function LayoutPanel({ id, rev, pages, current, guides, setGuides, run }: Props) {
  const [margins, setMargins] = useState<number[]>([20 * MM, 15 * MM, 20 * MM, 25 * MM])
  const [mode, setMode] = useState<'resize' | 'fit'>('fit')
  const [scope, setScope] = useState<'current' | 'all'>('all')
  const [hf, setHF] = useState<HFItem[]>([])
  const [labels, setLabels] = useState<Label[]>([])
  const [tocTitle, setTocTitle] = useState('Содержание')
  const [tocLevels, setTocLevels] = useState(2)

  useEffect(() => {
    api.layout(id).then((r) => {
      setHF(r.hf ?? [])
      setLabels(r.labels ?? [])
    })
  }, [id, rev])

  const page = pages[current]
  const newHF = (): HFItem => ({ text: 'Стр. {page} из {pages}', position: 'bottom-center', size: 9, offset: 12 * MM, side: 20 * MM, from: 0, to: 0, parity: 'all', skipFirst: false })
  const setItem = (i: number, patch: Partial<HFItem>) => setHF(hf.map((it, j) => (j === i ? { ...it, ...patch } : it)))
  const setLabel = (i: number, patch: Partial<Label>) => setLabels(labels.map((l, j) => (j === i ? { ...l, ...patch } : l)))

  return (
    <div class="layout-panel">
      <h4>Поля страниц</h4>
      <MarginInputs value={margins} set={setMargins} />
      <label class="check">
        <input type="checkbox" checked={!!guides} onChange={(e) => setGuides(e.currentTarget.checked ? margins : null)} /> показывать направляющие
      </label>
      <div class="seg small-seg">
        <button class={`btn small ${mode === 'fit' ? 'active' : ''}`} onClick={() => setMode('fit')}>
          Вписать содержимое
        </button>
        <button class={`btn small ${mode === 'resize' ? 'active' : ''}`} onClick={() => setMode('resize')}>
          Добавить к листу
        </button>
      </div>
      <p class="muted small">
        {mode === 'fit'
          ? 'Содержимое уменьшится и встанет внутрь полей, размер листа не изменится.'
          : 'Лист станет больше на указанные поля (отрицательные значения обрежут лист), содержимое останется на месте.'}
      </p>
      <div class="seg small-seg">
        <button class={`btn small ${scope === 'all' ? 'active' : ''}`} onClick={() => setScope('all')}>
          Все страницы
        </button>
        <button class={`btn small ${scope === 'current' ? 'active' : ''}`} onClick={() => setScope('current')}>
          Текущая ({current + 1})
        </button>
      </div>
      <button
        class="btn"
        onClick={() =>
          run(api.edit(id, { op: 'page.margins', page: 0, mode, pages: scope === 'current' && page ? [page.num] : [], margins: margins as [number, number, number, number] }))
        }
      >
        Применить поля
      </button>

      <h4>Колонтитулы</h4>
      <p class="muted small">
        Переменные: {'{page}'} — номер, {'{pages}'} — всего, {'{label}'} — метка страницы, {'{title}'} — заголовок документа, {'{date}'} — дата.
      </p>
      {hf.map((it, i) => (
        <div class="hf-item" key={i}>
          <input class="text-input" value={it.text} onInput={(e) => setItem(i, { text: e.currentTarget.value })} />
          <div class="hf-row">
            <select class="select" value={it.position} onChange={(e) => setItem(i, { position: e.currentTarget.value as HFItem['position'] })}>
              {POSITIONS.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
            <span class="num-field">
              <input value={String(it.size)} onChange={(e) => setItem(i, { size: num(e.currentTarget.value) || 9 })} />
              pt
            </span>
          </div>
          <div class="hf-row">
            <span class="num-field" title="Отступ от края листа">
              край <input value={mm(it.offset)} onChange={(e) => setItem(i, { offset: num(e.currentTarget.value) * MM })} />
            </span>
            <span class="num-field" title="Отступ слева и справа">
              бока <input value={mm(it.side)} onChange={(e) => setItem(i, { side: num(e.currentTarget.value) * MM })} /> мм
            </span>
          </div>
          <div class="hf-row">
            <select class="select" value={it.parity} onChange={(e) => setItem(i, { parity: e.currentTarget.value as HFItem['parity'] })}>
              <option value="all">все страницы</option>
              <option value="odd">нечётные</option>
              <option value="even">чётные</option>
            </select>
            <label class="small">
              <input type="checkbox" checked={it.skipFirst} onChange={(e) => setItem(i, { skipFirst: e.currentTarget.checked })} /> не на первой
            </label>
          </div>
          <div class="hf-row">
            <span class="num-field">
              с <input value={it.from || ''} placeholder="1" onChange={(e) => setItem(i, { from: num(e.currentTarget.value) || 0 })} />
            </span>
            <span class="num-field">
              по <input value={it.to || ''} placeholder={String(pages.length)} onChange={(e) => setItem(i, { to: num(e.currentTarget.value) || 0 })} />
            </span>
            <button class="btn ghost small" onClick={() => setHF(hf.filter((_, j) => j !== i))}>
              Удалить
            </button>
          </div>
        </div>
      ))}
      <div class="doc-actions">
        <button class="btn small" onClick={() => setHF([...hf, newHF()])}>
          Добавить колонтитул
        </button>
        <button class="btn small primary" onClick={() => run(api.edit(id, { op: 'hf', page: 0, hf }))}>
          Сохранить
        </button>
      </div>

      <h4>Нумерация страниц</h4>
      <p class="muted small">Метки, которые просмотрщики показывают вместо номеров: например, i–iii для вступления и 1, 2, 3 дальше.</p>
      {labels.map((l, i) => (
        <div class="hf-row" key={i}>
          <span class="num-field">
            со стр. <input value={String(l.start + 1)} onChange={(e) => setLabel(i, { start: Math.max(0, num(e.currentTarget.value) - 1) })} />
          </span>
          <select class="select narrow" value={l.style} onChange={(e) => setLabel(i, { style: e.currentTarget.value as Label['style'] })}>
            {STYLES.map(([v, t]) => (
              <option key={v} value={v}>
                {t}
              </option>
            ))}
          </select>
          <input class="text-input prefix" placeholder="префикс" value={l.prefix} onInput={(e) => setLabel(i, { prefix: e.currentTarget.value })} />
          <button class="btn ghost small" onClick={() => setLabels(labels.filter((_, j) => j !== i))}>
            ✕
          </button>
        </div>
      ))}
      <div class="doc-actions">
        <button class="btn small" onClick={() => setLabels([...labels, { start: labels.length ? current : 0, style: 'D', prefix: '', first: 1 }])}>
          Добавить диапазон
        </button>
        <button class="btn small primary" onClick={() => run(api.edit(id, { op: 'labels', page: 0, labels }))}>
          Сохранить
        </button>
      </div>

      <h4>Оглавление</h4>
      <p class="muted small">Страница со списком закладок, номерами страниц и ссылками. Сначала соберите закладки на вкладке «Закладки».</p>
      <label class="field">
        <span>Заголовок</span>
        <input value={tocTitle} onInput={(e) => setTocTitle(e.currentTarget.value)} />
      </label>
      <div class="hf-row">
        <span>Уровней</span>
        <select class="select narrow" value={tocLevels} onChange={(e) => setTocLevels(Number(e.currentTarget.value))}>
          <option value={1}>1</option>
          <option value={2}>2</option>
          <option value={3}>3</option>
        </select>
      </div>
      <div class="doc-actions">
        <button class="btn small" onClick={() => run(api.edit(id, { op: 'toc', page: 0, after: 0, toc: { title: tocTitle, maxLevel: tocLevels } }))}>
          В начало документа
        </button>
        <button
          class="btn small"
          onClick={() => page && run(api.edit(id, { op: 'toc', page: 0, after: page.num, toc: { title: tocTitle, maxLevel: tocLevels } }))}
        >
          После стр. {current + 1}
        </button>
      </div>
    </div>
  )
}
