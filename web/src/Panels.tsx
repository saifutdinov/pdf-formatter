import { api, fmtMM, type Block, type FontInfo, type PageInfo, type Progress } from './api'

const STATUS: Record<FontInfo['status'], [string, string]> = {
  embedded: ['встроен', 'ok'],
  subset: ['подмножество', 'warn'],
  missing: ['не встроен', 'bad'],
  standard: ['стандартный', 'info'],
}

export function FontBadge({ f }: { f: FontInfo }) {
  const [label, cls] = STATUS[f.status]
  return <span class={`badge ${f.hasText ? cls : 'bad'}`}>{f.hasText ? label : 'без текста'}</span>
}

export function fontFor(fonts: FontInfo[], ref?: number) {
  return ref ? fonts.find((f) => f.refs.includes(ref)) : undefined
}

const pageList = (pages: number[] | null) => {
  if (!pages?.length) return ''
  const shown = pages.slice(0, 8).map((p) => p + 1)
  return `стр. ${shown.join(', ')}${pages.length > 8 ? ` и ещё ${pages.length - 8}` : ''}`
}

function UploadFont({ onError }: { onError: (msg: string) => void }) {
  return (
    <label class="btn small">
      Загрузить шрифт
      <input
        type="file"
        accept=".ttf,.otf,.ttc"
        multiple
        hidden
        onChange={(e) => {
          const input = e.currentTarget
          if (input.files?.length) api.uploadFonts(input.files).catch((err) => onError(err.message))
          input.value = ''
        }}
      />
    </label>
  )
}

export function FontsPanel({ fonts, progress }: { fonts: FontInfo[]; progress: Progress | null }) {
  return (
    <div class="fonts-panel">
      <div class="fonts-panel-bar">
        <a href="#/fonts" class="small">
          Библиотека шрифтов →
        </a>
      </div>
      {progress && !progress.finished && (
        <div class="progress">
          <div style={{ width: `${(100 * progress.done) / Math.max(1, progress.total)}%` }} />
          <span>
            Анализ страниц: {progress.done} из {progress.total}
          </span>
        </div>
      )}
      {fonts.length === 0 && <p class="muted pad">{progress?.finished ? 'Текста в документе нет' : 'Собираю шрифты…'}</p>}
      {fonts.map((f) => (
        <div class="font-row" key={`${f.name}|${f.subtype}|${f.status}`}>
          <div class="font-head">
            <b>{f.name}</b> <FontBadge f={f} />
          </div>
          <div class="muted small">
            {f.subtype} · {pageList(f.pages)}
          </div>
          {f.problem && <div class={`font-problem ${f.blocking ? 'blocking' : ''}`}>{f.problem}</div>}
          {f.chars && <div class="font-chars">{f.chars.slice(0, 120)}</div>}
          {!f.match && (f.status === 'missing' || f.status === 'subset') && <UploadFont onError={alert} />}
        </div>
      ))}
    </div>
  )
}
