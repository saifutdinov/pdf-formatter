import { useEffect, useRef, useState } from 'preact/hooks'
import { api, passwords } from './api'

/**
 * Asks for the password of an encrypted document: to open it, or the owner
 * password to lift its restrictions. The password goes to the local server
 * and stays in memory.
 */
export function PasswordDialog({ id, owner, onDone, onCancel }: { id: string; owner: boolean; onDone: () => void; onCancel: () => void }) {
  const [pw, setPw] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => input.current?.focus(), [])
  const submit = (e: Event) => {
    e.preventDefault()
    setBusy(true)
    api
      .unlock(id, pw)
      .then(() => {
        if (!owner || !passwords.has(id)) passwords.set(id, pw)
        onDone()
      })
      .catch((err) => setError(err.message))
      .finally(() => setBusy(false))
  }
  return (
    <div class="modal-back">
      <form class="modal" onSubmit={submit}>
        <h3>{owner ? 'Пароль владельца' : 'Документ защищён паролем'}</h3>
        <p class="muted">
          {owner ? 'Владелец запретил изменять этот документ. Его пароль снимает ограничения.' : 'Введите пароль, чтобы открыть документ.'}
        </p>
        <input ref={input} type="password" value={pw} onInput={(e) => (setPw(e.currentTarget.value), setError(''))} autocomplete="off" />
        {error && <p class="error small">{error}</p>}
        <div class="modal-actions">
          <button type="button" class="btn ghost" onClick={onCancel}>
            Отмена
          </button>
          <button class="btn primary" disabled={busy}>
            {owner ? 'Снять ограничения' : 'Открыть'}
          </button>
        </div>
      </form>
    </div>
  )
}
