import { useState } from 'react'
import { api } from './api'
import { EnvValues } from './EnvValues'
import type { Global } from './types'
import { Field, Notice, Select, useAction } from './widgets'

const valid = (n: string) => /^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(n)

function keysOf(envs: string[], regions: string[]): string[] {
  return regions.length ? envs.flatMap(e => regions.map(r => `${e}/${r}`)) : [...envs]
}

function NameList({ title, names, noun, onAdd, onRemove, placeholder, taken }: {
  title: string; names: string[]; noun: string; onAdd: (n: string) => void; onRemove: (n: string) => void; placeholder: string; taken: string[]
}) {
  const [name, setName] = useState('')
  const ok = valid(name.trim()) && !taken.includes(name.trim())
  const add = () => { if (ok) { onAdd(name.trim()); setName('') } }
  return (
    <div className="card" data-list={noun}>
      <h4>{title}</h4>
      <div className="chips">
        {names.map(n => (
          <span className="chip" key={n}>{n}
            <button type="button" className="icon" title={`Remove ${noun} ${n}`} aria-label={`Remove ${noun} ${n}`} onClick={() => onRemove(n)}>×</button>
          </span>
        ))}
        {names.length === 0 && <span className="muted">None.</span>}
      </div>
      <div className="add-row">
        <input value={name} placeholder={placeholder} aria-label={`New ${noun} name`} onChange={e => setName(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') add() }} />
        <button type="button" onClick={add} disabled={!ok}>+ Add {noun}</button>
      </div>
    </div>
  )
}

/** The one list of environments and regions, and the values every application shares. */
export function GlobalEnvPanel({ global, onSaved }: { global: Global; onSaved: (g: Global) => void }) {
  const [g, setG] = useState<Global>(global)
  const [dirty, setDirty] = useState(false)
  const act = useAction()
  const envs = g.environments || []
  const regions = g.regions || []
  const change = (patch: Partial<Global>) => {
    const next = { ...g, ...patch }
    next.keys = keysOf(next.environments || [], next.regions || [])
    if (!next.keys.includes(next.defaultEnvironment || '')) {
      // Keep the same environment or region where possible.
      const [e, r] = (next.defaultEnvironment || '').split('/')
      next.defaultEnvironment = next.keys.find(k => k === e || k.startsWith(e + '/') || (r && k.endsWith('/' + r))) || next.keys[0]
    }
    setG(next); setDirty(true)
  }
  const save = () => act.run(async () => { const s = await api.saveGlobal(g); setG(s); setDirty(false); onSaved(s) })
  const removeEnv = (n: string) => {
    if (envs.length === 1) { act.setError('At least one environment is needed.'); return }
    if (confirm(`Remove environment “${n}” for every application? Their values for it are kept, but nothing can run against it any more.`)) change({ environments: envs.filter(e => e !== n) })
  }
  const removeRegion = (n: string) => {
    if (confirm(`Remove region “${n}” for every application? Their values for it are kept, but nothing can run against it any more.`)) change({ regions: regions.filter(r => r !== n) })
  }
  return (
    <div className="editor narrow">
      <div className="editor-bar">
        <div><h2>Environments and regions</h2><span className="muted mono">global/settings.yaml</span></div>
        <button type="button" className="primary" onClick={save} disabled={!dirty || act.busy}>{dirty ? 'Save' : 'Saved'}</button>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
      <p className="hint">One list for every application. A run targets {regions.length ? <>an environment and a region, for example <strong>{g.keys[0]}</strong></> : 'one environment'}. Auth profiles and certificate sets can hold different values for each target, and each application fills in its own addresses for them under its “Environments &amp; settings”.</p>
      <NameList title="Environments" noun="environment" names={envs} placeholder="new environment, e.g. uat" taken={[...envs, ...regions]}
        onAdd={n => change({ environments: [...envs, n] })} onRemove={removeEnv} />
      <NameList title="Regions" noun="region" names={regions} placeholder="new region, e.g. EU" taken={[...envs, ...regions]}
        onAdd={n => change({ regions: [...regions, n] })} onRemove={removeRegion} />
      <div className="card">
        <div className="grid">
          <Field label={regions.length ? 'Default environment and region' : 'Default environment'} hint="Used by applications that do not choose their own default.">
            <Select value={g.defaultEnvironment || ''} onChange={v => change({ defaultEnvironment: v })} options={g.keys} />
          </Field>
        </div>
      </div>

      <h3>Shared values</h3>
      <p className="hint">Variables every application and every auth profile can use as <code>{'{{name}}'}</code>, for example the identity provider’s address in each environment. An application’s own value with the same name wins. An application’s values are also available everywhere as <code>{'{{application-id.name}}'}</code>.</p>
      <EnvValues global={g} values={g.values} onChange={values => change({ values })} defaultKey={g.defaultEnvironment} noun="shared values" />
    </div>
  )
}
