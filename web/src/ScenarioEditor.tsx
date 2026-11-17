import { useEffect, useState } from 'react'
import { api, slug, uniqueId } from './api'
import type { Application, AuthProfile, CertSet, RunResult, Scenario, Test, TestType } from './types'
import { Field, KV, Notice, Section, Text, useAction } from './widgets'
import { AuthSelect, blankStep, KindBadge, MtlsSelect, TestEditor, TYPE_LABEL, TypeBadge } from './TestEditor'
import { RunView } from './RunView'
import { History } from './History'

const PLACEHOLDER = /^(step|case)(-\d+)?$/

/** Gives tests and cases created in this session ids derived from their names. */
function withIds(sc: Scenario): Scenario {
  const taken = sc.tests.filter(t => !PLACEHOLDER.test(t.id)).map(t => t.id)
  const tests = sc.tests.map(t => {
    let id = t.id
    if (PLACEHOLDER.test(id) && slug(t.name)) { id = uniqueId(slug(t.name), taken); taken.push(id) }
    const vTaken = (t.variants || []).filter(v => !PLACEHOLDER.test(v.id)).map(v => v.id)
    const variants = t.variants?.map(v => {
      if (!PLACEHOLDER.test(v.id) || !slug(v.name)) return v
      const vid = uniqueId(slug(v.name), vTaken); vTaken.push(vid)
      return { ...v, id: vid }
    })
    return { ...t, id, variants }
  })
  return { ...sc, tests }
}

export function ScenarioEditor({ app, scenarioId, profiles, sets, protos, env, onSaved, onDeleted }: {
  app: Application; scenarioId: string; profiles: AuthProfile[]; sets: CertSet[]; protos: string[]; env: string
  onSaved: (s: Scenario) => void; onDeleted: () => void
}) {
  const [sc, setSc] = useState<Scenario | null>(null)
  const [dirty, setDirty] = useState(false)
  const [open, setOpen] = useState<string | null>(null)
  const [mode, setMode] = useState<'form' | 'yaml'>('form')
  const [yaml, setYaml] = useState('')
  const [version, setVersion] = useState(0) // bumps to remount editors after a reload
  const [run, setRun] = useState<RunResult | null>(null)
  const [saved, setSaved] = useState(false)
  const load = useAction()
  const act = useAction()

  const reload = () => load.run(async () => {
    const list = await api.scenarios(app.id)
    const found = list.find(s => s.id === scenarioId)
    if (!found) throw new Error(`Scenario ${scenarioId} not found`)
    if (found.description?.startsWith('INVALID: ')) { setMode('yaml'); act.setError('This file does not parse: ' + found.description.slice(9) + '. Fix it in the YAML view.') }
    setSc({ ...found, tests: found.tests || [] })
    setYaml(await api.scenarioYaml(app.id, scenarioId))
    setDirty(false); setVersion(v => v + 1)
  })
  useEffect(() => { setSc(null); setRun(null); setOpen(null); setMode('form'); act.setError(''); reload() }, [app.id, scenarioId]) // eslint-disable-line

  if (load.error) return <Notice kind="error">{load.error}</Notice>
  if (!sc) return <p className="muted">Loading…</p>

  const change = (patch: Partial<Scenario>) => { setSc({ ...sc, ...patch }); setDirty(true); setSaved(false) }
  const setTest = (i: number, t: Test) => change({ tests: sc.tests.map((o, j) => (j === i ? t : o)) })
  const move = (i: number, d: number) => {
    const tests = [...sc.tests]; const j = i + d
    if (j < 0 || j >= tests.length) return
    ;[tests[i], tests[j]] = [tests[j], tests[i]]
    change({ tests })
  }
  const addTest = (type: TestType) => {
    const id = uniqueId('step', sc.tests.map(t => t.id))
    const t: Test = { id, name: '', type }
    ;(t as any)[type] = blankStep(type)
    change({ tests: [...sc.tests, t] }); setOpen(id)
  }
  const duplicate = (i: number) => {
    const src = sc.tests[i]
    const copy: Test = JSON.parse(JSON.stringify(src))
    copy.id = uniqueId(src.id + '-copy', sc.tests.map(t => t.id)); copy.name = src.name + ' (copy)'
    const tests = [...sc.tests]; tests.splice(i + 1, 0, copy)
    change({ tests }); setOpen(copy.id)
  }

  const save = async (): Promise<boolean> => {
    let ok = false
    await act.run(async () => {
      if (mode === 'yaml') {
        const s = await api.saveScenarioYaml(app.id, sc.id, yaml)
        setSc({ ...s, tests: s.tests || [] }); onSaved(s)
      } else {
        const unnamed = sc.tests.findIndex(t => !t.name.trim())
        if (unnamed >= 0) { setOpen(sc.tests[unnamed].id); throw new Error(`Step ${unnamed + 1} needs a name.`) }
        const next = withIds(sc)
        const s = await api.saveScenario(app.id, next)
        setSc({ ...s, tests: s.tests || [] }); onSaved(s)
        setYaml(await api.scenarioYaml(app.id, sc.id))
        if (open) { const idx = sc.tests.findIndex(t => t.id === open); setOpen(idx >= 0 ? next.tests[idx].id : null) }
      }
      setDirty(false); setSaved(true); setVersion(v => v + 1); ok = true
    })
    return ok
  }
  const runNow = async () => {
    if (dirty && !(await save())) return
    await act.run(async () => { setRun(null); setRun(await api.run(app.id, { environment: env, scenarios: [sc.id] })) })
  }
  const remove = () => {
    if (!confirm(`Delete scenario “${sc.name}”? This removes ${sc.id}.yaml.`)) return
    act.run(async () => { await api.deleteScenario(app.id, sc.id); onDeleted() })
  }
  const inherited = sc.auth || app.defaultAuth || 'none'
  const cases = sc.tests.reduce((n, t) => n + 1 + (t.variants?.length || 0), 0)

  return (
    <div className="editor">
      <div className="editor-bar">
        <div>
          <h2>{sc.name || sc.id}</h2>
          <span className="muted mono">scenarios/{sc.id}.yaml · {sc.tests.length} step{sc.tests.length === 1 ? '' : 's'}, {cases} test{cases === 1 ? '' : 's'} including cases</span>
        </div>
        <div className="row">
          <div className="seg" role="tablist">
            <button type="button" className={mode === 'form' ? 'on' : ''} onClick={() => { if (mode !== 'form' && (!dirty || confirm('Discard unsaved YAML edits?'))) { setMode('form'); if (dirty) reload() } }}>Form</button>
            <button type="button" className={mode === 'yaml' ? 'on' : ''} onClick={() => { if (mode !== 'yaml' && (!dirty || confirm('Switch to YAML? Unsaved form changes are discarded.'))) { setMode('yaml'); if (dirty) reload() } }}>YAML</button>
          </div>
          <button type="button" className="link danger" onClick={remove}>Delete</button>
          <button type="button" onClick={save} disabled={act.busy || !dirty}>{saved && !dirty ? 'Saved' : 'Save'}</button>
          <button type="button" className="primary" onClick={runNow} disabled={act.busy || sc.tests.length === 0}>{act.busy ? 'Working…' : dirty ? 'Save and run' : 'Run scenario'}</button>
        </div>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}

      {mode === 'yaml' ? (
        <div>
          <p className="hint">This is the file exactly as stored. It is validated when you save.</p>
          <textarea className="mono yaml" value={yaml} spellCheck={false} onChange={e => { setYaml(e.target.value); setDirty(true); setSaved(false) }} />
        </div>
      ) : (
        <div key={version}>
          <div className="card">
            <div className="grid">
              <Field label="Scenario name" wide><Text value={sc.name} onChange={name => change({ name })} /></Field>
              <Field label="Tags" hint="Comma separated. Runs can be filtered by tag."><Text value={(sc.tags || []).join(', ')} onChange={v => change({ tags: v.split(',').map(t => t.trim()).filter(Boolean) })} placeholder="smoke, orders" /></Field>
              <Field group label="Default auth for every step"><AuthSelect value={sc.auth} onChange={auth => change({ auth })} profiles={profiles} inherited={app.defaultAuth || 'none'} /></Field>
              <Field label="Default mutual TLS for every step"><MtlsSelect value={sc.mtls} onChange={mtls => change({ mtls })} sets={sets} inherited="" /></Field>
            </div>
            <Field label="Description" wide><Text value={sc.description} onChange={description => change({ description: description || undefined })} /></Field>
            <Section title="Variables" count={Object.keys(sc.variables || {}).length}>
              <p className="hint">Set once when the scenario starts and used as <code>{'{{name}}'}</code>. Built in: <code>{'{{$uuid}}'}</code>, <code>{'{{$timestamp}}'}</code>, <code>{'{{$isoTimestamp}}'}</code>, <code>{'{{$randomInt}}'}</code>, <code>{'{{$env.NAME}}'}</code>. Environment variables of the application ({Object.keys(app.environments?.[env] || {}).join(', ') || 'none yet'}) are available too.</p>
              <KV value={sc.variables} onChange={variables => change({ variables })} addLabel="Add variable" />
            </Section>
            <Section title="Properties" count={Object.keys(sc.properties || {}).length}>
              <KV value={sc.properties} onChange={properties => change({ properties })} keyLabel="property" addLabel="Add property" />
            </Section>
          </div>

          <h3>Steps <span className="muted">run top to bottom; a failed step stops the ones after it</span></h3>
          {sc.tests.length === 0 && <p className="muted">No steps yet. Add the first one below.</p>}
          {sc.tests.map((t, i) => (
            <div key={t.id} className={'test' + (open === t.id ? ' open' : '')}>
              <div className="test-head">
                <button type="button" className="test-title" onClick={() => setOpen(open === t.id ? null : t.id)} aria-expanded={open === t.id}>
                  <span className="order">{i + 1}</span>
                  <TypeBadge type={t.type} /> <KindBadge kind={t.kind} />
                  <span className="result-name">{t.name || <em className="muted">unnamed step</em>}</span>
                  {t.variants?.length ? <span className="muted">+{t.variants.length} case{t.variants.length === 1 ? '' : 's'}</span> : null}
                  {t.skip && <span className="muted">skipped</span>}
                </button>
                <div className="row tight">
                  <button type="button" className="icon" title="Move up" disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
                  <button type="button" className="icon" title="Move down" disabled={i === sc.tests.length - 1} onClick={() => move(i, 1)}>↓</button>
                  <button type="button" className="icon" title="Duplicate" onClick={() => duplicate(i)}>⧉</button>
                  <button type="button" className="icon" title="Delete step" onClick={() => { if (confirm(`Delete step “${t.name || t.id}”?`)) change({ tests: sc.tests.filter((_, j) => j !== i) }) }}>×</button>
                </div>
              </div>
              {open === t.id && <TestEditor test={t} onChange={nt => setTest(i, nt)} profiles={profiles} inheritedAuth={inherited} protos={protos} sets={sets} inheritedMtls={sc.mtls || ''} />}
            </div>
          ))}
          <div className="add-row">
            <span className="muted">Add step:</span>
            {(Object.keys(TYPE_LABEL) as TestType[]).map(type => <button type="button" key={type} onClick={() => addTest(type)}>+ {TYPE_LABEL[type]}</button>)}
          </div>
        </div>
      )}

      {run && (
        <div className="card">
          <h3>Last run</h3>
          <RunView run={run} />
        </div>
      )}
      <div className="card">
        <Section title="History of this scenario on this backend" open={false}>
          <History app={app.id} scenario={sc.id} envs={Object.keys(app.environments || {})} refresh={run?.id} />
        </Section>
      </div>
    </div>
  )
}
