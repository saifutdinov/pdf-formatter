import { useEffect, useState } from 'preact/hooks'
import { api, type Meta } from './api'

const fmtSize = (n: number) => (n < 1 << 20 ? `${Math.round(n / 1024)} КБ` : `${(n / (1 << 20)).toFixed(1)} МБ`)
const fmtDate = (s: string) => new Date(s).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' })

export function DocList() {
  const [docs, setDocs] = useState<Meta[] | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(0)
  const [drag, setDrag] = useState(false)
  const [standalone, setStandalone] = useState(false)
  const [stopped, setStopped] = useState(false)

  const reload = () => api.list().then(setDocs).catch((e) => setError(e.message))
  useEffect(() => {
    reload()
    fetch('/api/health')
      .then((r) => r.json())
      .then((h) => setStandalone(!!h.standalone))
      .catch(() => {})
  }, [])

  const quit = () => {
    if (!confirm('Выключить PDF Formatter? Все правки уже сохранены.')) return
    fetch('/api/quit', { method: 'POST' }).finally(() => setStopped(true))
  }

  if (stopped)
    return (
      <div class="list-page">
        <h1>PDF Formatter выключен</h1>
        <p class="muted">Все правки сохранены. Вкладку можно закрыть, а чтобы продолжить — снова запустите программу.</p>
      </div>
    )

  async function upload(files: FileList | File[]) {
    setError('')
    for (const f of Array.from(files)) {
      setBusy((b) => b + 1)
      try {
        await api.upload(f)
      } catch (e) {
        setError(`${f.name}: ${(e as Error).message}`)
      }
      setBusy((b) => b - 1)
    }
    reload()
  }

  async function remove(m: Meta) {
    if (!confirm(`Удалить «${m.name}»? Журнал правок тоже удалится.`)) return
    await api.remove(m.id).catch((e) => setError(e.message))
    reload()
  }

  return (
    <div
      class={`list-page ${drag ? 'drag' : ''}`}
      onDragOver={(e) => {
        e.preventDefault()
        setDrag(true)
      }}
      onDragLeave={() => setDrag(false)}
      onDrop={(e) => {
        e.preventDefault()
        setDrag(false)
        if (e.dataTransfer?.files.length) upload(e.dataTransfer.files)
      }}
    >
      <header class="list-header">
        <h1>PDF Formatter</h1>
        <span class="spacer" />
        <a class="btn ghost" href="#/fonts">
          Шрифты
        </a>
        {standalone && (
          <button class="btn ghost" onClick={quit} title="Остановить программу на этом компьютере">
            Выключить программу
          </button>
        )}
        <label class="btn primary">
          {busy ? 'Загружаю…' : 'Загрузить PDF'}
          <input
            type="file"
            accept="application/pdf,.pdf"
            multiple
            hidden
            onChange={(e) => {
              const input = e.currentTarget
              if (input.files?.length) upload(input.files)
              input.value = ''
            }}
          />
        </label>
      </header>
      {error && <div class="banner error">{error}</div>}
      {docs === null ? (
        <p class="muted">Загружаю список…</p>
      ) : docs.length === 0 ? (
        <div class="empty">Перетащите PDF сюда или нажмите «Загрузить PDF»</div>
      ) : (
        <table class="doc-table">
          <thead>
            <tr>
              <th>Документ</th>
              <th>Страниц</th>
              <th>Размер</th>
              <th>Изменён</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {docs.map((m) => (
              <tr key={m.id}>
                <td>
                  <a href={`#/doc/${m.id}`}>{m.name}</a>
                </td>
                <td>{m.pages}</td>
                <td>{fmtSize(m.size)}</td>
                <td>{fmtDate(m.modified)}</td>
                <td class="actions">
                  <button class="btn ghost" onClick={() => remove(m)} title="Удалить">
                    Удалить
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
