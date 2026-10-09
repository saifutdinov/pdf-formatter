import { render } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { DocList } from './DocList'
import { Editor } from './Editor'
import { FontsPage } from './FontsPage'
import './style.css'

function useHash() {
  const [hash, setHash] = useState(location.hash)
  useEffect(() => {
    const f = () => setHash(location.hash)
    addEventListener('hashchange', f)
    return () => removeEventListener('hashchange', f)
  }, [])
  return hash
}

function App() {
  const hash = useHash()
  const m = hash.match(/^#\/doc\/([0-9a-f]+)/)
  if (m) return <Editor id={m[1]} key={m[1]} />
  if (hash === '#/fonts') return <FontsPage />
  return <DocList />
}

render(<App />, document.getElementById('app')!)
