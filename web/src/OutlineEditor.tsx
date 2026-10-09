import { useEffect, useState } from 'preact/hooks'
import { api, type EditState, type OutlineItem, type PageInfo } from './api'

type Flat = { title: string; page: number; top?: number; level: number }

function flatten(items: OutlineItem[], level = 0, out: Flat[] = []): Flat[] {
  for (const it of items) {
    out.push({ title: it.title, page: it.page, top: it.top, level })
    flatten(it.items ?? [], level + 1, out)
  }
  return out
}

function nest(flat: Flat[]): OutlineItem[] {
  const root: OutlineItem[] = []
  const stack: { level: number; items: OutlineItem[] }[] = [{ level: -1, items: root }]
  for (const f of flat) {
    while (stack.length > 1 && stack[stack.length - 1].level >= f.level) stack.pop()
    const item: OutlineItem = { title: f.title, page: f.page, top: f.top, open: true, items: [] }
    stack[stack.length - 1].items.push(item)
    stack.push({ level: f.level, items: item.items! })
  }
  return root
}

type Props = {
  id: string
  rev?: number
  pages: PageInfo[]
  current: { page: number; top: number }
  onNav: (page: number, top?: number) => void
  run: (p: Promise<EditState>) => Promise<unknown>
}

/** Editable bookmark tree, kept flat with levels while editing. */
export function OutlineEditor({ id, rev, pages, current, onNav, run }: Props) {
  const [items, setItems] = useState<Flat[] | null>(null)
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    if (!dirty) api.outline(id).then((r) => setItems(flatten(r.outline ?? [])))
  }, [id, rev])
  if (!items) return <p class="muted pad">Загружаю…</p>

  const change = (next: Flat[]) => {
    setItems(next)
    setDirty(true)
  }
  const at = (i: number, patch: Partial<Flat>) => change(items.map((f, j) => (j === i ? { ...f, ...patch } : f)))
  const move = (i: number, d: -1 | 1) => {
    const j = i + d
    if (j < 0 || j >= items.length) return
    const next = [...items]
    ;[next[i], next[j]] = [next[j], next[i]]
    change(next)
  }
  const pageIndex = (num: number) => pages.findIndex((p) => p.num === num)

  return (
    <div class="outline-editor">
      <div class="outline-tools">
        <button
          class="btn small"
          onClick={() => {
            const p = pages[current.page]
            change([...items, { title: `Страница ${current.page + 1}`, page: p.num, top: Math.round(current.top), level: 0 }])
          }}
        >
          Закладка здесь
        </button>
        <button
          class="btn small"
          onClick={() =>
            api.outline(id, true).then((r) => {
              const s = flatten(r.outline ?? [])
              if (!s.length) return alert('Заголовков не найдено')
              if (!items.length || confirm(`Заменить закладки предложенными (${s.length})?`)) change(s)
            })
          }
        >
          Из заголовков
        </button>
      </div>
      {items.length === 0 && <p class="muted small">Закладок нет. Добавьте закладку на текущем месте или соберите их из заголовков.</p>}
      <ul class="outline-list">
        {items.map((f, i) => (
          <li key={i} style={{ paddingLeft: `${f.level * 14}px` }}>
            <input class="outline-title" value={f.title} onInput={(e) => at(i, { title: e.currentTarget.value })} />
            <button class="btn ghost small" title="Перейти" onClick={() => onNav(pageIndex(f.page), f.top)}>
              {pageIndex(f.page) + 1}
            </button>
            <span class="outline-btns">
              <button class="btn ghost small" title="Выше" onClick={() => move(i, -1)}>
                ↑
              </button>
              <button class="btn ghost small" title="Ниже" onClick={() => move(i, 1)}>
                ↓
              </button>
              <button class="btn ghost small" title="Уровень выше" disabled={f.level === 0} onClick={() => at(i, { level: f.level - 1 })}>
                ←
              </button>
              <button
                class="btn ghost small"
                title="Вложить"
                disabled={i === 0 || f.level > items[i - 1].level}
                onClick={() => at(i, { level: f.level + 1 })}
              >
                →
              </button>
              <button class="btn ghost small" title="Удалить" onClick={() => change(items.filter((_, j) => j !== i))}>
                ✕
              </button>
            </span>
          </li>
        ))}
      </ul>
      {dirty && (
        <div class="doc-actions">
          <button
            class="btn primary"
            onClick={() =>
              run(api.edit(id, { op: 'outline', page: 0, outline: nest(items.filter((f) => f.title.trim())) })).then(() => setDirty(false))
            }
          >
            Сохранить закладки
          </button>
          <button
            class="btn ghost"
            onClick={() => {
              setDirty(false)
              api.outline(id).then((r) => setItems(flatten(r.outline ?? [])))
            }}
          >
            Отменить
          </button>
        </div>
      )}
      <p class="muted small note">Просмотрщики откроют панель закладок сразу при открытии файла.</p>
    </div>
  )
}
