import type { Application, Asset, AuthCheckResult, AuthProfile, Backend, CertSet, RunQuery, RunResult, Scenario } from './types'

// ---- backend connections (kept in this browser only) ----

const KEY = 'testplane.backends'
const CURRENT = 'testplane.backend'
export const LOCAL: Backend = { id: 'local', name: 'This server', url: '' }

function read<T>(key: string, fallback: T): T {
  try { const v = localStorage.getItem(key); return v ? (JSON.parse(v) as T) : fallback } catch { return fallback }
}
function write(key: string, value: unknown) {
  try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* storage unavailable: keep in memory only */ }
}

let backends: Backend[] = (() => {
  const saved = read<Backend[]>(KEY, [])
  return saved.some(b => b.id === 'local') ? saved : [LOCAL, ...saved]
})()
let current: Backend = backends.find(b => b.id === read<string>(CURRENT, 'local')) || backends[0]

export const connections = {
  list: () => backends,
  current: () => current,
  save(b: Backend) {
    const clean = { ...b, url: b.url.trim().replace(/\/+$/, '') }
    backends = backends.some(x => x.id === b.id) ? backends.map(x => (x.id === b.id ? clean : x)) : [...backends, clean]
    if (current.id === b.id) current = clean
    write(KEY, backends)
  },
  remove(id: string) {
    if (id === 'local') return
    backends = backends.filter(b => b.id !== id)
    if (current.id === id) { current = backends[0]; write(CURRENT, current.id) }
    write(KEY, backends)
  },
  use(id: string) {
    current = backends.find(b => b.id === id) || backends[0]
    write(CURRENT, current.id)
  },
}

function headers(b: Backend, extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { ...extra }
  if (b.token) h.Authorization = 'Bearer ' + b.token
  if (b.user) h['X-Testplane-User'] = b.user
  return h
}

export class ApiError extends Error {
  constructor(message: string, public status: number) { super(message) }
}

async function send(b: Backend, method: string, path: string, body?: unknown): Promise<Response> {
  const init: RequestInit = { method, headers: headers(b) }
  if (body !== undefined) {
    if (typeof body === 'string' || body instanceof Blob || body instanceof ArrayBuffer) init.body = body as BodyInit
    else { init.body = JSON.stringify(body); init.headers = headers(b, { 'Content-Type': 'application/json' }) }
  }
  let res: Response
  try { res = await fetch(b.url + '/api' + path, init) } catch {
    throw new ApiError(`Cannot reach ${b.name}${b.url ? ` at ${b.url}` : ''}. Check the address, that it is running, and that it allows this page's origin.`, 0)
  }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try { msg = (await res.json()).error || msg } catch { /* not JSON */ }
    if (res.status === 401) msg = `${b.name} needs its access token. Open “Backends” and enter it.`
    throw new ApiError(msg, res.status)
  }
  return res
}

async function call<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const res = await send(current, method, path, body)
  if (res.status === 204) return undefined as T
  return (raw ? res.text() : res.json()) as Promise<T>
}

/** Checks an address and token before they are saved. */
export async function testBackend(b: Backend): Promise<string> {
  const meta = await (await send(b, 'GET', '/meta')).json()
  if (meta.name !== 'testplane') throw new ApiError('That address answers, but it is not a Testplane backend.', 0)
  const apps: Application[] = await (await send(b, 'GET', '/apps')).json()
  return `Connected: ${apps.length} application${apps.length === 1 ? '' : 's'}${meta.authRequired ? ', token accepted' : ', no token needed'}.`
}

const e = encodeURIComponent

export const api = {
  apps: () => call<Application[]>('GET', '/apps'),
  saveApp: (a: Application) => call<Application>('PUT', `/apps/${e(a.id)}`, a),
  deleteApp: (id: string) => call<void>('DELETE', `/apps/${e(id)}`),
  importApp: (zip: ArrayBuffer) => call<{ id: string }>('POST', '/import', zip),
  exportApp: async (id: string) => (await send(current, 'GET', `/apps/${e(id)}/export`)).blob(),

  scenarios: (app: string) => call<Scenario[]>('GET', `/apps/${e(app)}/scenarios`),
  saveScenario: (app: string, s: Scenario) => call<Scenario>('PUT', `/apps/${e(app)}/scenarios/${e(s.id)}`, s),
  deleteScenario: (app: string, id: string) => call<void>('DELETE', `/apps/${e(app)}/scenarios/${e(id)}`),
  scenarioYaml: (app: string, id: string) => call<string>('GET', `/apps/${e(app)}/scenarios/${e(id)}/yaml`, undefined, true),
  saveScenarioYaml: (app: string, id: string, yaml: string) => call<Scenario>('PUT', `/apps/${e(app)}/scenarios/${e(id)}/yaml`, yaml),

  auth: (app: string, env = '') => call<AuthProfile[]>('GET', `/apps/${e(app)}/auth?env=${e(env)}`),
  certSets: (app: string, env = '') => call<CertSet[]>('GET', `/apps/${e(app)}/tls?env=${e(env)}`),
  saveCertSet: (app: string, c: CertSet) => call<CertSet>('PUT', `/apps/${e(app)}/tls/${e(c.id)}`, c),
  deleteCertSet: (app: string, id: string) => call<void>('DELETE', `/apps/${e(app)}/tls/${e(id)}`),
  saveAuth: (app: string, p: AuthProfile) => call<AuthProfile>('PUT', `/apps/${e(app)}/auth/${e(p.id)}`, p),
  deleteAuth: (app: string, id: string) => call<void>('DELETE', `/apps/${e(app)}/auth/${e(id)}`),
  checkAuth: (app: string, id: string, env: string, mtls = '') => call<AuthCheckResult>('POST', `/apps/${e(app)}/auth/${e(id)}/check?env=${e(env)}&mtls=${e(mtls)}`),

  assets: (app: string) => call<Asset[]>('GET', `/apps/${e(app)}/assets`),
  uploadAsset: (app: string, category: string, name: string, data: ArrayBuffer) => call<void>('PUT', `/apps/${e(app)}/assets/${e(category)}/${e(name)}`, data),
  deleteAsset: (app: string, category: string, name: string) => call<void>('DELETE', `/apps/${e(app)}/assets/${e(category)}/${e(name)}`),

  run: (app: string, req: { environment?: string; scenarios?: string[]; kind?: string }) => call<RunResult>('POST', `/apps/${e(app)}/run`, req),
  runs: (app: string, q: RunQuery = {}) => {
    const p = Object.entries(q).filter(([, v]) => v !== undefined && v !== '').map(([k, v]) => `${k}=${e(String(v))}`).join('&')
    return call<RunResult[]>('GET', `/apps/${e(app)}/runs${p ? '?' + p : ''}`)
  },
  getRun: (app: string, id: string) => call<RunResult>('GET', `/apps/${e(app)}/runs/${e(id)}`),
}

export function slug(s: string): string {
  return s.toLowerCase().trim().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60)
}

export function uniqueId(base: string, taken: string[]): string {
  const b = base || 'item'
  if (!taken.includes(b)) return b
  for (let i = 2; ; i++) if (!taken.includes(`${b}-${i}`)) return `${b}-${i}`
}

export function when(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
