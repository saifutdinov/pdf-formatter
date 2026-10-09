import { useEffect, useMemo, useState } from 'preact/hooks'
import { api, type LibFont } from './api'
import { useLibFont } from './fontface'

const SOURCE = { builtin: 'встроенный', licensed: 'лицензионный', system: 'системный', upload: 'загружен' }
const SAMPLE = 'Съешь же ещё этих мягких французских булок, да выпей чаю. 0123456789'

function FontRow({ f, isDefault, onDefault, onDelete }: { f: LibFont; isDefault: boolean; onDefault: () => void; onDelete: () => void }) {
  const family = useLibFont(f.id)
  return (
    <div class={`lib-row ${isDefault ? 'default' : ''}`}>
      <div class="lib-meta">
        <b>{f.full}</b>
        <span class="muted small">
          <span title={f.source === 'licensed' ? 'Коммерческий шрифт: только по вашей лицензии, в публичные сборки не входит' : undefined}>
            {SOURCE[f.source]}
          </span>{' '}
          · {f.format.toUpperCase()}
          {!f.cyrillic && ' · без кириллицы'}
        </span>
      </div>
      <div class="lib-sample" style={{ fontFamily: family }}>
        {SAMPLE}
      </div>
      <div class="lib-actions">
        {isDefault ? (
          <span class="badge ok">по умолчанию</span>
        ) : (
          <button class="btn small" onClick={onDefault}>
            По умолчанию
          </button>
        )}
        {f.source === 'upload' && (
          <button class="btn small ghost" onClick={onDelete}>
            Удалить
          </button>
        )}
      </div>
    </div>
  )
}

export function FontsPage() {
  const [fonts, setFonts] = useState<LibFont[]>([])
  const [def, setDef] = useState('')
  const [ready, setReady] = useState(false)
  const [query, setQuery] = useState('')
  const [onlyCyr, setOnlyCyr] = useState(true)
  const [error, setError] = useState('')
  const [shown, setShown] = useState(60)

  const reload = () =>
    api
      .library()
      .then((r) => {
        setFonts(r.fonts)
        setDef(r.default)
        setReady(r.ready)
        if (!r.ready) setTimeout(reload, 1000)
      })
      .catch((e) => setError(e.message))
  useEffect(() => {
    reload()
  }, [])

  const list = useMemo(() => {
    const q = query.trim().toLowerCase()
    return fonts.filter((f) => (!onlyCyr || f.cyrillic) && (!q || `${f.full} ${f.psName}`.toLowerCase().includes(q)))
  }, [fonts, query, onlyCyr])
  const defFont = fonts.find((f) => f.id === def)

  return (
    <div class="list-page">
      <header class="list-header">
        <a class="btn ghost" href="#/">
          ← Документы
        </a>
        <h1>Библиотека шрифтов</h1>
        <label class="btn primary">
          Загрузить шрифт
          <input
            type="file"
            accept=".ttf,.otf,.ttc"
            multiple
            hidden
            onChange={(e) => {
              const input = e.currentTarget
              if (!input.files?.length) return
              setError('')
              api
                .uploadFonts(input.files)
                .then(reload)
                .catch((err) => setError(err.message))
              input.value = ''
            }}
          />
        </label>
      </header>
      {error && <div class="banner error">{error}</div>}
      <p class="muted">
        По умолчанию: <b>{defFont?.full ?? 'не выбран'}</b>. Им набирается новый текст и символы, которых нет в шрифте документа.
        {!ready && ' Индексирую шрифты…'}
      </p>
      <div class="lib-filters">
        <input class="search" placeholder="Поиск по названию" value={query} onInput={(e) => setQuery(e.currentTarget.value)} />
        <label>
          <input type="checkbox" checked={onlyCyr} onChange={(e) => setOnlyCyr(e.currentTarget.checked)} /> только с кириллицей
        </label>
        <span class="muted">{list.length} начертаний</span>
      </div>
      {list.slice(0, shown).map((f) => (
        <FontRow
          key={f.id}
          f={f}
          isDefault={f.id === def}
          onDefault={() => api.setDefaultFont(f.id).then(reload)}
          onDelete={() => confirm(`Удалить «${f.full}»?`) && api.removeFont(f.id).then(reload).catch((e) => setError(e.message))}
        />
      ))}
      {list.length > shown && (
        <button class="btn" onClick={() => setShown((n) => n + 60)}>
          Показать ещё
        </button>
      )}
    </div>
  )
}
