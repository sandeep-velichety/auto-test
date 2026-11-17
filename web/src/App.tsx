import { useCallback, useEffect, useState } from 'react'
import { api, connections, isConflict, slug, uniqueId } from './api'
import { Backends } from './Backends'
import { History } from './History'
import type { Application, Asset, AuthProfile, CertSet, Global, RunResult, Scenario, Usage } from './types'
import { AuthPanel } from './AuthPanel'
import { EnvPanel } from './EnvPanel'
import { GlobalEnvPanel } from './GlobalEnvPanel'
import { RunView } from './RunView'
import { ScenarioEditor } from './ScenarioEditor'
import { FileButton, Notice, Select, StatusBadge, useAction } from './widgets'

type Tab = 'scenarios' | 'settings' | 'runs'
type GlobalTab = 'auth' | 'environments'
/** Either an application (app + tab + item) or the Global area (global). */
interface Route { app?: string; tab: Tab; item?: string; global?: GlobalTab }

// "~global" cannot be an application id, so it is free to name the Global area.
const GLOBAL = '~global'

function parseHash(): Route {
  const [, app, tab, item] = window.location.hash.replace(/^#/, '').split('/').map(decodeURIComponent)
  if (app === GLOBAL) return { tab: 'scenarios', global: tab === 'environments' ? 'environments' : 'auth' }
  const tabs: Tab[] = ['scenarios', 'settings', 'runs']
  return { app: app || undefined, tab: tabs.includes(tab as Tab) ? (tab as Tab) : 'scenarios', item: item || undefined }
}

function go(r: Route) {
  const parts = r.global ? [GLOBAL, r.global] : [r.app, r.app ? r.tab : undefined, r.item]
  window.location.hash = '/' + parts.filter(Boolean).map(s => encodeURIComponent(s!)).join('/')
}

function RunsPanel({ app, envs, latest }: { app: Application; envs: string[]; latest: RunResult | null }) {
  return (
    <div className="editor">
      {latest && <div className="card"><h3>Run just finished</h3><RunView run={latest} /></div>}
      <h3>Run history on this backend</h3>
      <History app={app.id} envs={envs} refresh={latest?.id} />
    </div>
  )
}

const NO_USAGE: Usage = { auth: {}, certSets: {} }

export default function App() {
  const [route, setRoute] = useState<Route>(parseHash)
  const [apps, setApps] = useState<Application[] | null>(null)
  const [scenarios, setScenarios] = useState<Scenario[]>([])
  // Global: shared by every application.
  const [global, setGlobal] = useState<Global | null>(null)
  const [profiles, setProfiles] = useState<AuthProfile[]>([])
  const [sets, setSets] = useState<CertSet[]>([])
  const [certs, setCerts] = useState<Asset[]>([])
  const [usage, setUsage] = useState<Usage>(NO_USAGE)
  const [protos, setProtos] = useState<Asset[]>([])
  const [env, setEnv] = useState('')
  const [kind, setKind] = useState('')
  const [lastRun, setLastRun] = useState<RunResult | null>(null)
  const [newApp, setNewApp] = useState('')
  const [newScenario, setNewScenario] = useState('')
  const [find, setFind] = useState('')
  const [last, setLast] = useState<Record<string, { status: string; at: string }>>({})
  const [notice, setNotice] = useState('')
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
  const keys = global?.keys || []
  // Auth profiles, certificate sets and files. Certificate checks are for
  // the environment selected at the top.
  const loadGlobal = useCallback(async (forEnv: string) => {
    const [p, c, f, u] = await Promise.all([api.auth(forEnv), api.certSets(forEnv), api.certs(), api.usage().catch(() => NO_USAGE)])
    setProfiles(p); setSets(c); setCerts(f); setUsage(u)
  }, [])
  useEffect(() => {
    // Everything on screen belongs to the previous backend: clear it first.
    setApps(null); setGlobal(null); setScenarios([]); setProfiles([]); setSets([]); setCerts([]); setProtos([]); setUsage(NO_USAGE); setLastRun(null)
    top.run(async () => {
      const [list, g] = await Promise.all([loadApps(), api.global()])
      setGlobal(g)
      const r = parseHash()
      if (r.global) return
      if (!list.some(a => a.id === r.app)) go(list[0] ? { app: list[0].id, tab: 'scenarios' } : { tab: 'scenarios' })
      else if (r.item) go({ app: r.app, tab: r.tab })
    })
  }, [backend.id, backend.url, backend.token]) // eslint-disable-line

  const app = route.global ? undefined : apps?.find(a => a.id === route.app)
  const loadAppData = useCallback(async () => {
    if (!route.app) return
    const [s, p] = await Promise.all([api.scenarios(route.app), api.protos(route.app)])
    setScenarios(s); setProtos(p)
    // Latest result of each scenario, from recent history.
    api.runs(route.app, { limit: 30 }).then(runs => {
      const latest: Record<string, { status: string; at: string }> = {}
      for (const r of runs) for (const sc of r.scenarios) if (!latest[sc.id]) latest[sc.id] = { status: sc.status, at: r.startedAt }
      setLast(latest)
    }).catch(() => setLast({}))
  }, [route.app])
  useEffect(() => {
    setScenarios([]); setProtos([]); setLastRun(null)
  }, [app?.id]) // eslint-disable-line
  // The environment a view starts in: the application's default, else the global one.
  const startEnv = (app?.defaultEnvironment && keys.includes(app.defaultEnvironment) ? app.defaultEnvironment : '') || global?.defaultEnvironment || keys[0] || ''
  useEffect(() => { if (global) setEnv(startEnv) }, [app?.id, startEnv, !!global]) // eslint-disable-line
  const envReady = !!global && keys.includes(env)
  // The list can change under Global: never stay on a target that is gone.
  useEffect(() => { if (global && env && !keys.includes(env)) setEnv(startEnv) }, [keys.join()]) // eslint-disable-line
  useEffect(() => { if (app && envReady) top.run(loadAppData) }, [app?.id, envReady]) // eslint-disable-line
  useEffect(() => { if (envReady) top.run(() => loadGlobal(env)) }, [env, envReady]) // eslint-disable-line
  const reloadGlobal = () => loadGlobal(env)

  // Names are unique. Creating one that exists opens the existing one to
  // update it; nothing is created and nothing in it is lost.
  const openExisting = (kind: string, name: string) => confirm(`${kind} “${name}” already exists. Open it to update it? A second one will not be created.`)
  const createApp = () => top.run(async () => {
    const name = newApp.trim()
    if (!slug(name)) throw new Error('Give the application a name.')
    const open = (id: string) => { if (openExisting('Application', name)) { setNewApp(''); go({ app: id, tab: 'settings' }) } }
    const twin = (apps || []).find(a => a.name === name)
    if (twin) return open(twin.id)
    const id = uniqueId(slug(name), (apps || []).map(a => a.id))
    // Environments, auth and certificates are global: a new application only needs its own addresses.
    try { await api.saveApp({ id, name }) } catch (e) {
      if (!isConflict(e) || !e.existing) throw e
      await loadApps(); return open(e.existing) // someone else created it meanwhile
    }
    setNewApp(''); await loadApps(); go({ app: id, tab: 'settings' })
  })
  const createScenario = () => top.run(async () => {
    if (!app) return
    const name = newScenario.trim()
    if (!slug(name)) throw new Error('Give the scenario a name.')
    const open = (id: string) => { if (openExisting('Scenario', name)) { setNewScenario(''); go({ app: app.id, tab: 'scenarios', item: id }) } }
    const twin = scenarios.find(s => s.name === name)
    if (twin) return open(twin.id)
    const id = uniqueId(slug(name), scenarios.map(s => s.id))
    try { await api.saveScenario(app.id, { id, name, tests: [] }) } catch (e) {
      if (!isConflict(e) || !e.existing) throw e
      await loadAppData(); return open(e.existing)
    }
    setNewScenario(''); await loadAppData(); go({ app: app.id, tab: 'scenarios', item: id })
  })
  const runAll = () => runAct.run(async () => {
    if (!app) return
    const r = await api.run(app.id, { environment: env, kind: kind || undefined })
    setLastRun(r); go({ app: app.id, tab: 'runs' })
  })
  const importScenario = (f: File) => top.run(async () => {
    if (!app) return
    const text = await f.text()
    let res
    try { res = await api.importScenario(app.id, text) } catch (e) {
      if (!isConflict(e)) throw e
      const have = scenarios.find(s => s.id === e.existing)
      if (!confirm(`Scenario “${have?.name || e.existing}” already exists in this application. Update it with the file? Its current tests will be replaced.`)) return
      res = await api.importScenario(app.id, text, true)
    }
    await Promise.all([loadAppData(), reloadGlobal()])
    setNotice(`Imported “${res.scenario}”.` + (res.created.length ? ` Created under Global: ${res.created.join(', ')}.` : '') + (res.kept.length ? ` Kept the existing global: ${res.kept.join(', ')}.` : '') + (res.missing?.length ? ` Still needed here: ${res.missing.join(', ')}.` : ''))
    go({ app: app.id, tab: 'scenarios', item: res.scenario })
  })
  const importZip = (f: File) => top.run(async () => {
    const zip = await f.arrayBuffer()
    let r
    try { r = await api.importApp(zip) } catch (e) {
      if (!isConflict(e)) throw e
      const have = (apps || []).find(a => a.id === e.existing)
      if (!confirm(`Application “${have?.name || e.existing}” already exists. Update it with the zip? Its settings and the scenarios in the zip will be replaced; global auth and certificates that already exist are kept.`)) return
      r = await api.importApp(zip, true)
    }
    await Promise.all([loadApps(), reloadGlobal()]); go({ app: r.id, tab: 'scenarios' })
  })

  // With regions, a target is "environment/region": offer two pickers.
  const paired = (global?.regions || []).length > 0
  const [envPart, regionPart] = paired ? [env.split('/')[0], env.split('/').slice(1).join('/')] : [env, '']
  const envPicker = paired ? (
    <>
      <label className="inline">Environment <Select value={envPart} onChange={e => setEnv(`${e}/${regionPart}`)} options={global?.environments || []} /></label>
      <label className="inline">Region <Select value={regionPart} onChange={r => setEnv(`${envPart}/${r}`)} options={global?.regions || []} /></label>
    </>
  ) : (
    <label className="inline">Environment <Select value={env} onChange={setEnv} options={keys.length ? keys : [['', '(none defined)']]} /></label>
  )
  const deleteApp = () => {
    if (!app || !confirm(`Delete application “${app.name}” with all its scenarios and run history? Global auth and certificates are not touched. This cannot be undone.`)) return
    top.run(async () => { await api.deleteApp(app.id); const list = await loadApps(); await reloadGlobal(); go(list[0] ? { app: list[0].id, tab: 'scenarios' } : { tab: 'scenarios' }) })
  }
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
        <div className="side-label">Shared by all applications</div>
        <button type="button" className={'side-item global' + (route.global ? ' on' : '')} onClick={() => go({ tab: 'scenarios', global: 'auth' })} title="Auth, certificates, environments and regions used by every application">
          <span>Global</span>
          <span className="side-sub">auth · certificates · environments</span>
        </button>
        <div className="side-label">Applications</div>
        <nav>
          {(apps || []).map(a => (
            <button type="button" key={a.id} className={'side-item' + (!route.global && a.id === route.app ? ' on' : '')} onClick={() => go({ app: a.id, tab: 'scenarios' })}>{a.name || a.id}</button>
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
        {route.global && global && (
          <>
            <header className="app-head">
              <div>
                <h1>Global</h1>
                <p className="muted">Auth profiles, certificates, and the list of environments and regions. Set up once here; every application and everyone on this backend uses the same ones.</p>
              </div>
              {route.global === 'auth' && <div className="row"><span className="muted">Show certificate checks for</span>{envPicker}</div>}
            </header>
            <div className="tabs" role="tablist">
              {([['auth', `Auth & certificates (${profiles.length + sets.length})`], ['environments', `Environments & regions (${keys.length})`]] as [GlobalTab, string][]).map(([t, label]) => (
                <button type="button" role="tab" aria-selected={route.global === t} key={t} className={route.global === t ? 'on' : ''} onClick={() => go({ tab: 'scenarios', global: t })}>{label}</button>
              ))}
            </div>
            {route.global === 'auth' && <AuthPanel key={backend.id} apps={apps || []} profiles={profiles} sets={sets} assets={certs} envs={keys} env={env} usage={usage} reload={reloadGlobal} />}
            {route.global === 'environments' && <GlobalEnvPanel key={backend.id} global={global} onSaved={g => setGlobal(g)} />}
          </>
        )}
        {apps && !app && !route.global && (
          <div className="empty">
            <h2>{apps.length ? 'Pick an application' : 'Create your first application'}</h2>
            <p>An application is one system under test. It holds its own addresses for each environment and its scenarios, as YAML files in one folder you can share. Auth profiles, certificates and the environment list are set up once under Global and used by every application.</p>
          </div>
        )}
        {app && (
          <>
            <header className="app-head">
              <div>
                <h1>{app.name}</h1>
                {app.description && <p className="muted">{app.description}</p>}
                <button type="button" className="link danger" onClick={deleteApp}>Delete application</button>
              </div>
              <div className="row">
                {envPicker}
                <label className="inline">Run <Select value={kind} onChange={setKind} options={[['', 'everything'], ['positive', 'positive only'], ['negative', 'steps + negative cases']]} /></label>
                <button type="button" className="primary" onClick={runAll} disabled={runAct.busy || scenarios.length === 0}>{runAct.busy ? 'Running…' : `Run all ${scenarios.length} scenario${scenarios.length === 1 ? '' : 's'}`}</button>
              </div>
            </header>
            {runAct.error && <Notice kind="error" onClose={() => runAct.setError('')}>{runAct.error}</Notice>}
            <div className="tabs" role="tablist">
              {([['scenarios', `Scenarios (${scenarios.length})`], ['settings', 'Environments & settings'], ['runs', 'Runs']] as [Tab, string][]).map(([t, label]) => (
                <button type="button" role="tab" aria-selected={route.tab === t} key={t} className={route.tab === t ? 'on' : ''} onClick={() => go({ app: app.id, tab: t })}>{label}</button>
              ))}
            </div>

            {route.tab === 'scenarios' && (
              <div className="split">
                <div className="list">
                  <div className="list-head"><h3>Scenarios</h3><FileButton label="Import…" accept=".yaml,.yml" onFile={importScenario} /></div>
                  {notice && <Notice kind="ok" onClose={() => setNotice('')}>{notice}</Notice>}
                  {scenarios.length > 4 && <input value={find} placeholder="Filter by name or tag" aria-label="Filter scenarios" onChange={e => setFind(e.target.value)} />}
                  {scenarios.filter(s => !find.trim() || (s.name + ' ' + s.id + ' ' + (s.tags || []).join(' ')).toLowerCase().includes(find.trim().toLowerCase())).map(s => (
                    <button type="button" key={s.id} className={'list-item' + (s.id === route.item ? ' on' : '')} onClick={() => go({ app: app.id, tab: 'scenarios', item: s.id })}>
                      <span className="result-name">{s.name || s.id} {last[s.id] && <StatusBadge status={last[s.id].status} />}</span>
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
                    ? <ScenarioEditor key={backend.id + route.item} app={app} scenarioId={route.item} profiles={profiles} sets={sets} protos={protos.map(a => a.name)} env={env} envs={keys}
                        onSaved={() => { loadAppData(); reloadGlobal() }} onDeleted={async () => { await loadAppData(); await reloadGlobal(); go({ app: app.id, tab: 'scenarios' }) }} />
                    : <p className="muted">{route.item && scenarios.length ? `Scenario “${route.item}” does not exist.` : 'Select a scenario, or create one.'}</p>}
                </div>
              </div>
            )}
            {route.tab === 'settings' && global && <EnvPanel key={backend.id + app.id + keys.join()} app={app} global={global} profiles={profiles} protos={protos} reloadProtos={loadAppData}
              openGlobal={t => go({ tab: 'scenarios', global: t })} onSaved={() => { loadApps(); reloadGlobal() }} onDeleted={async () => { await loadApps(); go({ tab: 'scenarios' }) }} />}
            {route.tab === 'runs' && <RunsPanel app={app} envs={keys} latest={lastRun} />}
          </>
        )}
      </main>
    </div>
  )
}
