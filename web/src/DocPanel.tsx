import { useEffect, useState } from 'preact/hooks'
import { api, type DocMeta, type EditState, type Sanitize } from './api'

// RFC 3339 ↔ value of <input type="datetime-local"> (local time).
const toLocal = (s: string) => {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}
const fromLocal = (s: string) => {
  if (!s) return ''
  const d = new Date(s)
  const off = -d.getTimezoneOffset()
  const p = (n: number) => String(Math.floor(Math.abs(n))).padStart(2, '0')
  const tz = `${off >= 0 ? '+' : '-'}${p(off / 60)}:${p(off % 60)}`
  return `${s.length === 16 ? `${s}:00` : s}${tz}`
}

const FIELDS: [keyof DocMeta, string][] = [
  ['title', 'Заголовок'],
  ['author', 'Автор'],
  ['subject', 'Тема'],
  ['keywords', 'Ключевые слова'],
  ['creator', 'Создано в'],
  ['producer', 'PDF создан'],
  ['lang', 'Язык'],
]

export function DocPanel({ id, rev, run }: { id: string; rev?: number; run: (p: Promise<EditState>) => Promise<unknown> }) {
  const [meta, setMeta] = useState<DocMeta | null>(null)
  const [san, setSan] = useState<Sanitize>({ pieceInfo: true, thumbnails: true, javascript: true, embedded: false })
  const [autoMod, setAutoMod] = useState(true)
  const [custom, setCustom] = useState<[string, string][]>([])
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api.meta(id).then((r) => {
      setMeta(r.meta)
      setCustom(Object.entries(r.meta.custom ?? {}))
    })
  }, [id, rev])

  if (!meta) return <p class="muted pad">Загружаю…</p>
  const set = (patch: Partial<DocMeta>) => setMeta({ ...meta, ...patch })
  const save = () => {
    const m: DocMeta = { ...meta, custom: Object.fromEntries(custom.filter(([k, v]) => k.trim() && v.trim())) }
    if (autoMod) m.modDate = fromLocal(toLocal(new Date().toISOString()))
    run(api.edit(id, { op: 'meta', page: 0, meta: m })).then(() => {
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    })
  }

  return (
    <div class="doc-panel">
      <h4>Свойства документа</h4>
      {FIELDS.map(([k, label]) => (
        <label class="field" key={k}>
          <span>{label}</span>
          <input value={meta[k] as string} onInput={(e) => set({ [k]: e.currentTarget.value })} placeholder={k === 'lang' ? 'ru-RU' : ''} />
        </label>
      ))}
      <label class="field">
        <span>Создан</span>
        <input type="datetime-local" value={toLocal(meta.creationDate)} onInput={(e) => set({ creationDate: fromLocal(e.currentTarget.value) })} />
      </label>
      <label class="field">
        <span>Изменён</span>
        <input type="datetime-local" disabled={autoMod} value={toLocal(meta.modDate)} onInput={(e) => set({ modDate: fromLocal(e.currentTarget.value) })} />
      </label>
      <label class="check">
        <input type="checkbox" checked={autoMod} onChange={(e) => setAutoMod(e.currentTarget.checked)} /> ставить текущее время при сохранении
      </label>
      <label class="check">
        <input type="checkbox" checked={meta.displayDocTitle} onChange={(e) => set({ displayDocTitle: e.currentTarget.checked })} /> показывать заголовок в
        окне вместо имени файла
      </label>
      <h5>Свои поля</h5>
      {custom.map(([k, v], i) => (
        <div class="custom-row" key={i}>
          <input value={k} placeholder="Ключ (например, Location)" onInput={(e) => setCustom(custom.map((c, j) => (j === i ? [e.currentTarget.value, c[1]] : c)))} />
          <input value={v} placeholder="Значение" onInput={(e) => setCustom(custom.map((c, j) => (j === i ? [c[0], e.currentTarget.value] : c)))} />
          <button class="btn ghost small" onClick={() => setCustom(custom.filter((_, j) => j !== i))}>
            ✕
          </button>
        </div>
      ))}
      <button class="btn small" onClick={() => setCustom([...custom, ['', '']])}>
        Добавить поле
      </button>
      <div class="doc-actions">
        <button class="btn primary" onClick={save}>
          Сохранить
        </button>
        {saved && <span class="muted small">Сохранено</span>}
      </div>

      <h4>Очистка скрытых данных</h4>
      {(
        [
          ['pieceInfo', 'Данные других программ (PieceInfo)'],
          ['thumbnails', 'Встроенные миниатюры страниц'],
          ['javascript', 'JavaScript и автоматические действия'],
          ['embedded', 'Вложенные файлы'],
        ] as [keyof Sanitize, string][]
      ).map(([k, label]) => (
        <label class="check" key={k}>
          <input type="checkbox" checked={san[k]} onChange={(e) => setSan({ ...san, [k]: e.currentTarget.checked })} /> {label}
        </label>
      ))}
      <button class="btn" onClick={() => run(api.edit(id, { op: 'sanitize', page: 0, sanitize: san }))}>
        Очистить
      </button>
      <p class="muted small note">
        Обычное «Скачать» дописывает изменения в конец файла: старые значения метаданных и удалённое содержимое остаются в предыдущих ревизиях
        и легко извлекаются. Для передачи файла другим людям используйте «Чистая копия».
      </p>
    </div>
  )
}
