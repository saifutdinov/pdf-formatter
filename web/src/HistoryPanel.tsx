import { useEffect, useState } from 'preact/hooks'
import { api, type EditState, type HistoryEntry } from './api'

const KIND: Record<string, string> = { text: 'текст', rect: 'прямоугольник', ellipse: 'эллипс', line: 'линия', image: 'картинка', svg: 'SVG', table: 'таблица' }

const OPS: Record<string, string> = {
  transform: 'Перемещение',
  recolor: 'Цвет',
  delete: 'Удаление',
  update: 'Изменение',
  meta: 'Свойства документа',
  sanitize: 'Очистка скрытых данных',
  link: 'Ссылка',
  outline: 'Закладки',
  labels: 'Нумерация страниц',
  hf: 'Колонтитулы',
  'page.insert': 'Вставка страницы',
  'page.delete': 'Удаление страницы',
  'page.move': 'Перестановка страниц',
  'page.rotate': 'Поворот страницы',
  'page.margins': 'Поля страниц',
  'page.import': 'Страницы из PDF',
  toc: 'Оглавление',
  security: 'Защита',
  field: 'Поле формы',
  flatten: 'Фиксация полей',
  'table.split': 'Перенос таблицы',
}

function label(e: HistoryEntry): string {
  if (e.op === 'add') {
    if (!e.new) return e.kind === 'image' || e.kind === 'svg' ? 'Замена картинки' : 'Правка текста'
    return `Добавлен объект${e.kind ? `: ${KIND[e.kind] ?? e.kind}` : ''}`
  }
  return OPS[e.op] ?? e.op
}

const time = (at?: string) => (at ? new Date(at).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' }) : '')

/** Every step of the edit log; a click goes to that state in one change. */
export function HistoryPanel({ id, rev, run }: { id: string; rev?: number; run: (p: Promise<EditState>) => Promise<unknown> }) {
  const [h, setH] = useState<{ done: HistoryEntry[]; undone: HistoryEntry[] } | null>(null)
  useEffect(() => {
    api.history(id).then(setH)
  }, [id, rev])
  if (!h) return <p class="muted pad">Загружаю…</p>
  const row = (e: HistoryEntry | null, n: number, state: 'done' | 'current' | 'undone') => (
    <li key={n} class={`history-row ${state}`} onClick={() => state !== 'current' && run(api.goto(id, n))} title={state === 'current' ? 'Текущее состояние' : 'Перейти к этому шагу'}>
      <span class="history-label">
        {e ? label(e) : 'Исходный документ'}
        {e?.text && <span class="muted small"> · {e.text}</span>}
      </span>
      <span class="muted small">
        {e && e.page >= 0 ? `стр. ${e.page + 1} ` : ''}
        {time(e?.at)}
      </span>
    </li>
  )
  return (
    <div class="history">
      <ol>
        {row(null, 0, h.done.length === 0 ? 'current' : 'done')}
        {h.done.map((e, i) => row(e, i + 1, i === h.done.length - 1 ? 'current' : 'done'))}
        {h.undone.map((e, j) => row(e, h.done.length + j + 1, 'undone'))}
      </ol>
      <p class="muted small pad">Щелчок по шагу возвращает документ в то состояние; отменённые шаги остаются в списке, пока не сделана новая правка.</p>
    </div>
  )
}
