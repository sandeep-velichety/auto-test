import { useState } from 'react'
import { api } from './api'
import type { Application, AuthProfile } from './types'
import { Field, KV, Notice, Select, Text, useAction } from './widgets'

export function EnvPanel({ app, profiles, onSaved, onDeleted }: { app: Application; profiles: AuthProfile[]; onSaved: (a: Application) => void; onDeleted: () => void }) {
  const [a, setA] = useState<Application>(app)
  const [dirty, setDirty] = useState(false)
  const [newEnv, setNewEnv] = useState('')
  const [version, setVersion] = useState(0)
  const act = useAction()
  const set = (patch: Partial<Application>) => { setA({ ...a, ...patch }); setDirty(true) }
  const envs = a.environments || {}
  const names = Object.keys(envs)
  const addEnv = () => {
    const n = newEnv.trim()
    if (!n || envs[n]) return
    const copyFrom = a.defaultEnvironment && envs[a.defaultEnvironment] ? { ...envs[a.defaultEnvironment] } : {}
    set({ environments: { ...envs, [n]: copyFrom }, defaultEnvironment: a.defaultEnvironment || n })
    setNewEnv(''); setVersion(v => v + 1)
  }
  const save = () => act.run(async () => { const s = await api.saveApp(a); setA(s); setDirty(false); onSaved(s) })
  return (
    <div className="editor narrow">
      <div className="editor-bar">
        <div><h2>Application settings</h2><span className="muted mono">applications/{a.id}/application.yaml</span></div>
        <button type="button" className="primary" onClick={save} disabled={!dirty || act.busy}>{dirty ? 'Save' : 'Saved'}</button>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
      <div className="card">
        <div className="grid">
          <Field label="Name" wide><Text value={a.name} onChange={name => set({ name })} /></Field>
          <Field label="Default environment"><Select value={a.defaultEnvironment || ''} onChange={v => set({ defaultEnvironment: v || undefined })} options={[['', '(none)'], ...names]} /></Field>
          <Field label="Default auth profile" hint="Used by every test that does not pick its own.">
            <Select value={a.defaultAuth || ''} onChange={v => set({ defaultAuth: v || undefined })} options={[['', 'none'], ...profiles.map(p => [p.id, p.name || p.id] as [string, string])]} />
          </Field>
        </div>
        <Field label="Description" wide><Text value={a.description} onChange={description => set({ description: description || undefined })} /></Field>
      </div>

      <h3>Environments</h3>
      <p className="hint">Each environment is a set of variables such as <code>baseUrl</code>, <code>grpcTarget</code>, <code>kafkaBrokers</code>, <code>mongoUri</code> or <code>postgresDsn</code>. Tests refer to them as <code>{'{{baseUrl}}'}</code>, so the same scenarios run against any environment.</p>
      {names.map(n => (
        <div className="card" key={n + version}>
          <div className="editor-bar">
            <h4>{n}{a.defaultEnvironment === n ? <span className="muted"> · default</span> : null}</h4>
            <button type="button" className="link danger" onClick={() => {
              if (!confirm(`Remove environment “${n}” and its variables?`)) return
              const next = { ...envs }; delete next[n]
              set({ environments: next, defaultEnvironment: a.defaultEnvironment === n ? Object.keys(next)[0] : a.defaultEnvironment })
            }}>Remove</button>
          </div>
          <KV value={envs[n]} onChange={v => set({ environments: { ...envs, [n]: v || {} } })} keyLabel="variable" addLabel="Add variable" />
        </div>
      ))}
      <div className="add-row">
        <input value={newEnv} placeholder="new environment name, e.g. staging" onChange={e => setNewEnv(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') addEnv() }} />
        <button type="button" onClick={addEnv} disabled={!newEnv.trim() || !!envs[newEnv.trim()]}>+ Add environment</button>
      </div>

      <h3>Share or remove</h3>
      <div className="row">
        <button type="button" disabled={act.busy} onClick={() => act.run(async () => {
          const url = URL.createObjectURL(await api.exportApp(a.id))
          const link = document.createElement('a')
          link.href = url; link.download = a.id + '.zip'; link.click()
          setTimeout(() => URL.revokeObjectURL(url), 1000)
        })}>Download as zip</button>
        <span className="hint">Scenarios, auth profiles and certificates. Private keys are left out and stored secrets are replaced with CHANGE_ME.</span>
      </div>
      <div className="row">
        <button type="button" className="danger" onClick={() => { if (confirm(`Delete application “${a.name}” with all its scenarios, auth profiles and certificates? This cannot be undone.`)) act.run(async () => { await api.deleteApp(a.id); onDeleted() }) }}>Delete application</button>
      </div>
    </div>
  )
}
