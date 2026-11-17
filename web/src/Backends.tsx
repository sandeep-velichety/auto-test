import { useState } from 'react'
import { connections, testBackend, uniqueId, slug } from './api'
import type { Backend } from './types'
import { Field, Notice, Text, useAction } from './widgets'

/** Add, edit, test and switch the backends this browser connects to. */
export function Backends({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const [list, setList] = useState<Backend[]>(connections.list())
  const [currentId, setCurrentId] = useState(connections.current().id)
  const [edit, setEdit] = useState<Backend>(connections.current())
  const [isNew, setIsNew] = useState(false)
  const [ok, setOk] = useState('')
  const act = useAction()
  const set = (patch: Partial<Backend>) => { setEdit({ ...edit, ...patch }); setOk('') }
  const pick = (b: Backend) => { setEdit(b); setIsNew(false); setOk(''); act.setError('') }
  const isLocal = edit.id === 'local'

  const prepared = (): Backend => {
    const url = edit.url.trim().replace(/\/+$/, '')
    if (!isLocal && !/^https?:\/\/[^/]+/.test(url)) throw new Error('Enter the backend address, starting with http:// or https://')
    const name = edit.name.trim() || url
    const id = isNew ? uniqueId(slug(name) || 'backend', list.map(b => b.id)) : edit.id
    return { id, name, url: isLocal ? '' : url, token: edit.token?.trim() || undefined, user: edit.user?.trim() || undefined }
  }
  const test = () => act.run(async () => { setOk(''); setOk(await testBackend(prepared())) })
  const save = (connect: boolean) => act.run(async () => {
    const b = prepared()
    setOk(await testBackend(b))
    connections.save(b)
    if (connect) connections.use(b.id)
    setList([...connections.list()]); setCurrentId(connections.current().id); setEdit(b); setIsNew(false)
    onChanged()
  })
  const remove = () => {
    if (!confirm(`Remove “${edit.name}” from this browser? Nothing is deleted on the backend itself.`)) return
    connections.remove(edit.id)
    setList([...connections.list()]); setCurrentId(connections.current().id); pick(connections.current()); onChanged()
  }

  return (
    <div className="modal-back" role="dialog" aria-modal="true" aria-label="Backends">
      <div className="modal">
        <div className="editor-bar">
          <div><h2>Backends</h2><span className="muted">A backend holds the applications, certificates, secrets and run history, and runs the tests. Connections are remembered in this browser only.</span></div>
          <button type="button" onClick={onClose}>Close</button>
        </div>
        <div className="split modal-split">
          <div className="list">
            {list.map(b => (
              <button type="button" key={b.id} className={'list-item' + (!isNew && edit.id === b.id ? ' on' : '')} onClick={() => pick(b)}>
                <span className="result-name">{b.name}{b.id === currentId ? ' · connected' : ''}</span>
                <span className="muted">{b.url || 'the server this page came from'}</span>
              </button>
            ))}
            <button type="button" onClick={() => { setEdit({ id: '', name: '', url: 'https://' }); setIsNew(true); setOk(''); act.setError('') }}>+ Add backend</button>
          </div>
          <div>
            {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
            {ok && <Notice kind="ok">{ok}</Notice>}
            <Field label="Name"><Text value={edit.name} onChange={name => set({ name })} placeholder="QA backend" disabled={isLocal} /></Field>
            <Field label="Address" hint={isLocal ? 'The server that served this page.' : 'Where that backend’s “testplane serve” is reachable. It must be started with a token, or with --allow-origin for this page.'}>
              <Text mono value={isLocal ? window.location.origin : edit.url} onChange={url => set({ url })} placeholder="https://tests.qa.example.com" disabled={isLocal} />
            </Field>
            <Field label="Access token" hint="Given to you by whoever runs the backend. Leave empty if it has none."><Text type="password" value={edit.token} onChange={token => set({ token })} /></Field>
            <Field label="Your name" hint="Recorded with every run you start, so history shows who ran what."><Text value={edit.user} onChange={user => set({ user })} placeholder="e.g. anudeep" /></Field>
            <div className="row">
              <button type="button" onClick={test} disabled={act.busy}>Test connection</button>
              {(isNew || edit.id !== currentId) && <button type="button" onClick={() => save(false)} disabled={act.busy}>Save</button>}
              <button type="button" className="primary" onClick={() => save(true)} disabled={act.busy}>{edit.id === currentId && !isNew ? 'Save' : 'Save and connect'}</button>
              {!isNew && !isLocal && <button type="button" className="link danger" onClick={remove}>Remove</button>}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
