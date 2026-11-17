import { useState } from 'react'
import { api, connections, copy, download, isConflict } from './api'
import { EnvValues } from './EnvValues'
import type { Application, Asset, AuthProfile, Backend, Global } from './types'
import { Badge, CopyToBackend, Field, FileButton, Notice, Select, Text, useAction } from './widgets'

/** An application's own settings: its values for the global environments, and its proto files. */
export function EnvPanel({ app, global, profiles, protos, onSaved, onDeleted, reloadProtos, openGlobal }: {
  app: Application; global: Global; profiles: AuthProfile[]; protos: Asset[]
  onSaved: (a: Application) => void; onDeleted: () => void; reloadProtos: () => Promise<void>; openGlobal: (tab: 'auth' | 'environments') => void
}) {
  const [a, setA] = useState<Application>(app)
  const [dirty, setDirty] = useState(false)
  const [copied, setCopied] = useState('')
  const act = useAction()
  // Another backend gets the application and its scenarios; it keeps its own
  // auth, certificates and its own values for the environment it runs.
  const copyTo = (target: Backend) => act.run(async () => {
    setCopied('')
    if (dirty) throw new Error('Save your changes first, so the copy has them.')
    let res
    try { res = await copy.app(target, a.id) } catch (e) {
      if (!isConflict(e)) throw e
      if (!confirm(`“${a.name}” already exists on ${target.name}. Update it there? Its settings and scenarios are updated from here; ${target.name} keeps its own auth, certificates, and its own values for the environment it runs.`)) return
      res = await copy.app(target, a.id, true)
    }
    setCopied(`Copied “${a.name}” to ${target.name}.` + (res.missing.length ? ` Still to set up there under Global: ${res.missing.join(', ')}.` : ' Nothing else needs setting up there.'))
  })
  const set = (patch: Partial<Application>) => { setA({ ...a, ...patch }); setDirty(true) }
  const paired = (global.regions || []).length > 0
  const save = () => act.run(async () => { const s = await api.saveApp(a); setA(s); setDirty(false); onSaved(s) })
  const unknownAuth = a.defaultAuth && !profiles.some(p => p.id === a.defaultAuth)
  const unknownEnv = a.defaultEnvironment && !global.keys.includes(a.defaultEnvironment)
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
          <Field label={paired ? 'Default environment and region' : 'Default environment'}>
            <Select value={a.defaultEnvironment || ''} onChange={v => set({ defaultEnvironment: v || undefined })}
              options={[['', `global default (${global.defaultEnvironment || global.keys[0] || 'none'})`], ...global.keys,
                ...(unknownEnv ? [[a.defaultEnvironment!, `${a.defaultEnvironment} · not in the global list`] as [string, string]] : [])]} />
          </Field>
          <Field label="Default auth profile" hint="A global profile, used by every test that does not pick its own.">
            <Select value={a.defaultAuth || ''} onChange={v => set({ defaultAuth: v || undefined })}
              options={[['', 'none'], ...profiles.map(p => [p.id, p.name || p.id] as [string, string]),
                ...(unknownAuth ? [[a.defaultAuth!, `${a.defaultAuth} · does not exist`] as [string, string]] : [])]} />
          </Field>
        </div>
        <Field label="Description" wide><Text value={a.description} onChange={description => set({ description: description || undefined })} /></Field>
        <p className="hint">Auth profiles and certificates are not kept per application: they are global and every application can use them. <button type="button" className="link" onClick={() => openGlobal('auth')}>Open Global auth &amp; certificates</button></p>
      </div>

      <h3>This application’s values per environment</h3>
      <p className="hint">
        The list of environments{paired ? ' and regions' : ''} is global: <strong>{(global.environments || []).join(', ')}</strong>{paired ? <> across <strong>{(global.regions || []).join(', ')}</strong></> : null}.{' '}
        <button type="button" className="link" onClick={() => openGlobal('environments')}>Change the list under Global</button>
      </p>
      <p className="hint">Fill in this application’s own variables for each one, such as <code>baseUrl</code>, <code>grpcTarget</code>, <code>kafkaBrokers</code>, <code>mongoUri</code> or <code>postgresDsn</code>. Tests refer to them as <code>{'{{baseUrl}}'}</code>, so the same scenarios run anywhere. <code>{'{{$environment}}'}</code>{paired ? <> and <code>{'{{$region}}'}</code> give the names</> : ' gives the name'} being run. Global shared values are available too.</p>
      <EnvValues global={global} values={a.environments} onChange={environments => set({ environments })} defaultKey={a.defaultEnvironment || global.defaultEnvironment} noun="values" />

      <h3>Proto files</h3>
      <p className="hint">For gRPC tests of this application that cannot use server reflection.</p>
      <div className="row"><FileButton label="Upload .proto" accept=".proto" onFile={f => act.run(async () => { await api.uploadProto(a.id, f.name, await f.arrayBuffer()); await reloadProtos() })} /></div>
      {protos.map(p => (
        <div className="asset" key={p.name}>
          <div><span className="mono">{p.name}</span> <Badge kind="neutral">proto</Badge></div>
          <button type="button" className="icon" title={`Delete ${p.name}`} onClick={() => { if (confirm(`Delete ${p.name}? gRPC tests that use it will stop working.`)) act.run(async () => { await api.deleteProto(a.id, p.name); await reloadProtos() }) }}>×</button>
        </div>
      ))}
      {protos.length === 0 && <p className="muted">None uploaded.</p>}

      <h3>Share or remove</h3>
      {copied && <Notice kind="ok" onClose={() => setCopied('')}>{copied}</Notice>}
      <div className="row">
        <CopyToBackend what="application" others={connections.list().filter(b => b.id !== connections.current().id)} onCopy={copyTo} disabled={act.busy} />
        <span className="hint">Sends this application and its scenarios to another backend, for example from stage to prod. Auth, certificates and secrets are not copied: each backend keeps its own.</span>
      </div>
      <div className="row">
        <button type="button" disabled={act.busy} onClick={() => act.run(async () => {
          download(await api.exportApp(a.id), a.id + '.zip')
        })}>Download as zip</button>
        <span className="hint">The application with its scenarios, plus a copy of the global auth profiles and certificates they use. Private keys are left out and stored secrets are replaced with CHANGE_ME.</span>
      </div>
      <div className="row">
        <button type="button" className="danger" onClick={() => { if (confirm(`Delete application “${a.name}” with all its scenarios and run history? Global auth and certificates are not touched. This cannot be undone.`)) act.run(async () => { await api.deleteApp(a.id); onDeleted() }) }}>Delete application</button>
      </div>
    </div>
  )
}
