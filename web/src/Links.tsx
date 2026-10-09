import { useState } from 'preact/hooks'
import type { Link, PageInfo } from './api'

type Target = { kind: 'uri' | 'page'; uri: string; page: string }

function TargetFields({ t, set, pages }: { t: Target; set: (t: Target) => void; pages: PageInfo[] }) {
  return (
    <>
      <div class="seg small-seg link-kind">
        <button class={`btn small ${t.kind === 'uri' ? 'active' : ''}`} onClick={() => set({ ...t, kind: 'uri' })}>
          Адрес
        </button>
        <button class={`btn small ${t.kind === 'page' ? 'active' : ''}`} onClick={() => set({ ...t, kind: 'page' })}>
          Страница
        </button>
      </div>
      {t.kind === 'uri' ? (
        <input class="text-input" placeholder="https://… или mailto:…" value={t.uri} onInput={(e) => set({ ...t, uri: e.currentTarget.value })} />
      ) : (
        <span class="num-field">
          <input value={t.page} onInput={(e) => set({ ...t, page: e.currentTarget.value })} /> из {pages.length}
        </span>
      )}
    </>
  )
}

/** Converts form fields into link fields; null when invalid. */
function toLink(t: Target, pages: PageInfo[]): Partial<Link> | null {
  if (t.kind === 'uri') {
    const uri = t.uri.trim()
    return /^(https?:\/\/|mailto:|tel:)/i.test(uri) ? { kind: 'uri', uri } : null
  }
  const n = Number(t.page)
  return n >= 1 && n <= pages.length ? { kind: 'page', page: pages[n - 1].num } : null
}

const pageNumber = (pages: PageInfo[], num?: number) => String(pages.findIndex((p) => p.num === num) + 1 || 1)

/** Link section for a selected block or object. */
export function AttachLink({ link, pages, onSave, onDelete }: { link?: Link; pages: PageInfo[]; onSave: (l: Partial<Link>) => void; onDelete: () => void }) {
  const [t, setT] = useState<Target>({ kind: link?.kind === 'page' ? 'page' : 'uri', uri: link?.uri ?? 'https://', page: pageNumber(pages, link?.page) })
  const [open, setOpen] = useState(false)
  if (!link && !open)
    return (
      <button class="btn small" onClick={() => setOpen(true)}>
        Добавить ссылку
      </button>
    )
  const l = toLink(t, pages)
  return (
    <div class="link-box">
      <TargetFields t={t} set={setT} pages={pages} />
      <div class="link-actions">
        <button class="btn small primary" disabled={!l} onClick={() => l && onSave(l)}>
          {link ? 'Сохранить ссылку' : 'Создать ссылку'}
        </button>
        {link && (
          <button class="btn small ghost" onClick={onDelete}>
            Убрать
          </button>
        )}
      </div>
    </div>
  )
}

/** Properties of a selected link. */
export function LinkProps({ link, pages, onSave, onDelete, onFollow }: { link: Link; pages: PageInfo[]; onSave: (l: Partial<Link>) => void; onDelete: () => void; onFollow: () => void }) {
  const [t, setT] = useState<Target>({ kind: link.kind === 'page' ? 'page' : 'uri', uri: link.uri ?? '', page: pageNumber(pages, link.page) })
  const l = toLink(t, pages)
  return (
    <aside class="props">
      <h3>Ссылка</h3>
      {link.kind === 'other' && <p class="muted small">Действие этой ссылки редактор не понимает: его можно только заменить или удалить.</p>}
      <TargetFields t={t} set={setT} pages={pages} />
      <div class="link-actions">
        <button class="btn small primary" disabled={!l} onClick={() => l && onSave(l)}>
          Сохранить
        </button>
        <button class="btn small" onClick={onFollow}>
          Перейти
        </button>
      </div>
      {link.attach && <p class="muted small">Ссылка привязана к блоку и двигается вместе с ним.</p>}
      <button class="btn danger" onClick={onDelete}>
        Удалить ссылку
      </button>
    </aside>
  )
}
