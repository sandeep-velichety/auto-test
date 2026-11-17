import { useCallback, useEffect, useState } from 'react'
import { api, connections, slug, uniqueId } from './api'
import { Backends } from './Backends'
import { History } from './History'
import type { Application, Asset, AuthProfile, RunResult, Scenario } from './types'
import { AuthPanel } from './AuthPanel'
import { EnvPanel } from './EnvPanel'
import { RunView } from './RunView'
import { ScenarioEditor } from './ScenarioEditor'
import { FileButton, Notice, Select, useAction } from './widgets'

type Tab = 'scenarios' | 'auth' | 'settings' | 'runs'
interface Route { app?: string; tab: Tab; item?: string }

function parseHash(): Route {
  const [, app, tab, item] = window.location.hash.replace(/^#/, '').split('/').map(decodeURIComponent)
  const tabs: Tab[] = ['scenarios', 'auth', 'settings', 'runs']
  return { app: app || undefined, tab: tabs.includes(tab as Tab) ? (tab as Tab) : 'scenarios', item: item || undefined }
}

function go(r: Route) {
  window.location.hash = '/' + [r.app, r.app ? r.tab : undefined, r.item].filter(Boolean).map(s => encodeURIComponent(s!)).join('/')
}

function RunsPanel({ app, latest }: { app: Application; latest: RunResult | null }) {
  return (
    <div className="editor">
      {latest && <div className="card"><h3>Run just finished</h3><RunView run={latest} /></div>}
      <h3>Run history on this backend</h3>
      <History app={app.id} envs={Object.keys(app.environments || {})} refresh={latest?.id} />
    </div>
  )
}

export default function App() {
  const [route, setRoute] = useState<Route>(parseHash)
  const [apps, setApps] = useState<Application[] | null>(null)
  const [scenarios, setScenarios] = useState<Scenario[]>([])
  const [profiles, setProfiles] = useState<AuthProfile[]>([])
  const [assets, setAssets] = useState<Asset[]>([])
  const [env, setEnv] = useState('')
  const [kind, setKind] = useState('')
  const [lastRun, setLastRun] = useState<RunResult | null>(null)
  const [newApp, setNewApp] = useState('')
  const [newScenario, setNewScenario] = useState('')
  const [others, setOthers] = useState<AuthProfile[]>([])
  const [showBackends, setShowBackends] = useState(false)
  const [backend, setBackend] = useState(connections.current())
  const top = useAction()
  const runAct = useAction()

  useEffect(() => {
    const on = () => setRoute(parseHash())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])

  const loadApps = useCallback(async () => {
    const list = await api.apps()
    setApps(list)
    return list
  }, [])
  useEffect(() => {
    // Everything on screen belongs to the previous backend: clear it first.
    setApps(null); setScenarios([]); setProfiles([]); setAssets([]); setOthers([]); setLastRun(null)
    top.run(async () => {
      const list = await loadApps()
      const r = parseHash()
      if (!list.some(a => a.id === r.app)) go(list[0] ? { app: list[0].id, tab: 'scenarios' } : { tab: 'scenarios' })
      else if (r.item) go({ app: r.app, tab: r.tab })
    })
  }, [backend.id, backend.url, backend.token]) // eslint-disable-line

  const app = apps?.find(a => a.id === route.app)
  const loadAppData = useCallback(async () => {
    if (!route.app) return
    const [s, p, a] = await Promise.all([api.scenarios(route.app), api.auth(route.app), api.assets(route.app)])
    setScenarios(s); setProfiles(p); setAssets(a)
    // Profiles of the other applications, usable in flow scenarios as "app/profile".
    const rest = (apps || []).filter(o => o.id !== route.app)
    const lists = await Promise.all(rest.map(o => api.auth(o.id).catch(() => [] as AuthProfile[])))
    setOthers(lists.flatMap((list, i) => list.filter(x => !x.description?.startsWith('INVALID: ')).map(x => ({ ...x, id: rest[i].id + '/' + x.id, name: `${rest[i].name || rest[i].id} / ${x.name || x.id}` }))))
  }, [route.app, apps])
  useEffect(() => {
    setScenarios([]); setProfiles([]); setAssets([]); setLastRun(null)
    if (app) top.run(loadAppData)
  }, [app?.id]) // eslint-disable-line
  useEffect(() => { setEnv(app?.defaultEnvironment || Object.keys(app?.environments || {})[0] || '') }, [app?.id, app?.defaultEnvironment]) // eslint-disable-line

  const createApp = () => top.run(async () => {
    const id = uniqueId(slug(newApp), (apps || []).map(a => a.id))
    if (!slug(newApp)) throw new Error('Give the application a name.')
    await api.saveApp({ id, name: newApp.trim(), defaultEnvironment: 'local', environments: { local: { baseUrl: 'http://localhost:8080' } } })
    setNewApp(''); await loadApps(); go({ app: id, tab: 'settings' })
  })
  const createScenario = () => top.run(async () => {
    if (!app) return
    if (!slug(newScenario)) throw new Error('Give the scenario a name.')
    const id = uniqueId(slug(newScenario), scenarios.map(s => s.id))
    await api.saveScenario(app.id, { id, name: newScenario.trim(), tests: [] })
    setNewScenario(''); await loadAppData(); go({ app: app.id, tab: 'scenarios', item: id })
  })
  const runAll = () => runAct.run(async () => {
    if (!app) return
    const r = await api.run(app.id, { environment: env, kind: kind || undefined })
    setLastRun(r); go({ app: app.id, tab: 'runs' })
  })
  const importZip = (f: File) => top.run(async () => { const r = await api.importApp(await f.arrayBuffer()); await loadApps(); go({ app: r.id, tab: 'scenarios' }) })

  const envNames = Object.keys(app?.environments || {})
  const selected = scenarios.find(s => s.id === route.item)

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">Testplane</div>
        <div className="side-label">Backend</div>
        <button type="button" className="side-item backend" onClick={() => setShowBackends(true)} title="Switch or add a backend">
          <span>{backend.name}</span>
          <span className="side-sub">{backend.url || 'this server'} · change</span>
        </button>
        <div className="side-label">Applications</div>
        <nav>
          {(apps || []).map(a => (
            <button type="button" key={a.id} className={'side-item' + (a.id === route.app ? ' on' : '')} onClick={() => go({ app: a.id, tab: 'scenarios' })}>{a.name || a.id}</button>
          ))}
          {apps && apps.length === 0 && <p className="side-empty">No applications yet.</p>}
        </nav>
        <div className="side-form">
          <input value={newApp} placeholder="New application name" onChange={e => setNewApp(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') createApp() }} />
          <button type="button" onClick={createApp} disabled={!newApp.trim() || top.busy}>Create</button>
          <FileButton label="Import zip…" accept=".zip" onFile={importZip} />
        </div>
      </aside>

      {showBackends && <Backends onClose={() => setShowBackends(false)} onChanged={() => setBackend({ ...connections.current() })} />}
      <main className="main">
        {top.error && <Notice kind="error" onClose={() => top.setError('')}>{top.error}</Notice>}
        {!apps && !top.error && <p className="muted">Loading…</p>}
        {apps && !app && (
          <div className="empty">
            <h2>{apps.length ? 'Pick an application' : 'Create your first application'}</h2>
            <p>An application is one system under test. It holds its environments, its auth profiles and certificates, and its scenarios, all as YAML files in one folder you can share.</p>
          </div>
        )}
        {app && (
          <>
            <header className="app-head">
              <div>
                <h1>{app.name}</h1>
                {app.description && <p className="muted">{app.description}</p>}
              </div>
              <div className="row">
                <label className="inline">Environment <Select value={env} onChange={setEnv} options={envNames.length ? envNames : [['', '(none defined)']]} /></label>
                <label className="inline">Run <Select value={kind} onChange={setKind} options={[['', 'everything'], ['positive', 'positive only'], ['negative', 'steps + negative cases']]} /></label>
                <button type="button" className="primary" onClick={runAll} disabled={runAct.busy || scenarios.length === 0}>{runAct.busy ? 'Running…' : `Run all ${scenarios.length} scenario${scenarios.length === 1 ? '' : 's'}`}</button>
              </div>
            </header>
            {runAct.error && <Notice kind="error" onClose={() => runAct.setError('')}>{runAct.error}</Notice>}
            <div className="tabs" role="tablist">
              {([['scenarios', `Scenarios (${scenarios.length})`], ['auth', `Auth & assets (${profiles.length})`], ['settings', 'Environments & settings'], ['runs', 'Runs']] as [Tab, string][]).map(([t, label]) => (
                <button type="button" role="tab" aria-selected={route.tab === t} key={t} className={route.tab === t ? 'on' : ''} onClick={() => go({ app: app.id, tab: t })}>{label}</button>
              ))}
            </div>

            {route.tab === 'scenarios' && (
              <div className="split">
                <div className="list">
                  <div className="list-head"><h3>Scenarios</h3></div>
                  {scenarios.map(s => (
                    <button type="button" key={s.id} className={'list-item' + (s.id === route.item ? ' on' : '')} onClick={() => go({ app: app.id, tab: 'scenarios', item: s.id })}>
                      <span className="result-name">{s.name || s.id}</span>
                      <span className="muted">{s.description?.startsWith('INVALID: ') ? 'file does not parse' : `${(s.tests || []).length} step${(s.tests || []).length === 1 ? '' : 's'}${s.tags?.length ? ' · ' + s.tags.join(', ') : ''}`}</span>
                    </button>
                  ))}
                  {scenarios.length === 0 && <p className="muted">No scenarios yet. A scenario is an ordered list of tests that tell one story, for example “create an order, check the database, wait for the event”.</p>}
                  <div className="side-form light">
                    <input value={newScenario} placeholder="New scenario name" onChange={e => setNewScenario(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') createScenario() }} />
                    <button type="button" onClick={createScenario} disabled={!newScenario.trim() || top.busy}>Create scenario</button>
                  </div>
                </div>
                <div className="detail">
                  {route.item && selected
                    ? <ScenarioEditor key={backend.id + route.item} app={app} scenarioId={route.item} profiles={[...profiles, ...others]} protos={assets.filter(a => a.category === 'protos').map(a => a.name)} env={env}
                        onSaved={() => loadAppData()} onDeleted={async () => { await loadAppData(); go({ app: app.id, tab: 'scenarios' }) }} />
                    : <p className="muted">{route.item && scenarios.length ? `Scenario “${route.item}” does not exist.` : 'Select a scenario, or create one.'}</p>}
                </div>
              </div>
            )}
            {route.tab === 'auth' && <AuthPanel key={backend.id + app.id} app={app} profiles={profiles} assets={assets} env={env} reload={loadAppData} />}
            {route.tab === 'settings' && <EnvPanel key={backend.id + app.id} app={app} profiles={profiles} onSaved={() => loadApps()} onDeleted={async () => { await loadApps(); go({ tab: 'scenarios' }) }} />}
            {route.tab === 'runs' && <RunsPanel app={app} latest={lastRun} />}
          </>
        )}
      </main>
    </div>
  )
}
