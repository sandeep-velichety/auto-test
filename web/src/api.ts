import type { Application, Asset, AuthCheckResult, AuthProfile, Backend, CertSet, Global, GrpcService, RunQuery, RunResult, Scenario, Usage } from './types'

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
  /** Records what a backend says it runs, so the environment pickers can find it. */
  setEnvironment(id: string, environment: string | undefined) {
    const b = backends.find(x => x.id === id)
    if (!b || (b.environment || '') === (environment || '')) return
    this.save({ ...b, environment: environment || undefined })
  },
  /** A saved backend limited to exactly this environment (or to all of its regions). */
  backendFor(env: string): Backend | undefined {
    return backends.find(b => b.environment === env) || backends.find(b => !!b.environment && covers(b.environment, env))
  },
}

/** Whether a backend limited to scope may run env (same rule as the backend's). */
export function covers(scope: string | undefined, env: string): boolean {
  return !scope || scope === env || (!scope.includes('/') && env.split('/')[0] === scope)
}

function headers(b: Backend, extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { ...extra }
  if (b.token) h.Authorization = 'Bearer ' + b.token
  if (b.user) h['X-Testplane-User'] = b.user
  return h
}

export class ApiError extends Error {
  /** For 409 (already exists): the id of the item that has the name. */
  existing?: string
  constructor(message: string, public status: number) { super(message) }
}

/** True when the backend refused something because it already exists. */
export const isConflict = (e: unknown): e is ApiError => e instanceof ApiError && e.status === 409

/**
 * Creates something that has a full form (an auth profile, a certificate
 * set). If one with that name already exists, asks whether to update it
 * with the values entered instead of making a second one.
 * knownId is the existing item's id when the caller already sees it in its
 * list; the backend's own check covers lists that are out of date.
 * Returns undefined when the person declines.
 */
export async function createOrUpdate<T>(kind: string, name: string, knownId: string | undefined, save: (existingId?: string) => Promise<T>): Promise<T | undefined> {
  const ask = () => confirm(`${kind} “${name}” already exists. Update it with these values instead of creating a second one? Its current settings will be replaced.`)
  if (knownId) return ask() ? save(knownId) : undefined
  try { return await save() } catch (e) {
    if (!isConflict(e) || !e.existing) throw e
    return ask() ? save(e.existing) : undefined
  }
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
    let existing: string | undefined
    try { const body = await res.json(); msg = body.error || msg; existing = body.existing || undefined } catch { /* not JSON */ }
    if (res.status === 401) msg = `${b.name} needs its access token. Open “Backends” and enter it.`
    const err = new ApiError(msg, res.status)
    err.existing = existing
    throw err
  }
  return res
}

async function call<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const res = await send(current, method, path, body)
  if (res.status === 204) return undefined as T
  return (raw ? res.text() : res.json()) as Promise<T>
}

const e = encodeURIComponent

/** Checks an address and token before they are saved. */
export async function testBackend(b: Backend): Promise<{ message: string; environment?: string }> {
  const meta = await (await send(b, 'GET', '/meta')).json()
  if (meta.name !== 'testplane') throw new ApiError('That address answers, but it is not a Testplane backend.', 0)
  const apps: Application[] = await (await send(b, 'GET', '/apps')).json()
  const runs = meta.environment ? `runs ${String(meta.environment).replace('/', ' · ')} only` : 'runs any environment'
  return { message: `Connected: ${runs}, ${apps.length} application${apps.length === 1 ? '' : 's'}${meta.authRequired ? ', token accepted' : ', no token needed'}.`, environment: meta.environment || undefined }
}

/**
 * Copies from the connected backend to another one. Only applications and
 * scenarios travel: auth, certificates and secrets stay where they are, and
 * the answer lists what the other backend still has to set up ("missing").
 */
export const copy = {
  /** replace: update the application if the other backend already has it. */
  async app(target: Backend, appId: string, replace = false): Promise<{ id: string; missing: string[] }> {
    const zip = await (await send(current, 'GET', `/apps/${e(appId)}/export?global=false`)).arrayBuffer()
    const res = await (await send(target, 'POST', `/import?replace=${replace}`, zip)).json()
    return { id: res.id, missing: res.missing || [] }
  },
  async scenario(target: Backend, appId: string, scenarioId: string, overwrite = false): Promise<{ scenario: string; missing: string[] }> {
    const yaml = await (await send(current, 'GET', `/apps/${e(appId)}/scenarios/${e(scenarioId)}/yaml`)).text()
    const res = await (await send(target, 'POST', `/apps/${e(appId)}/scenarios/import?overwrite=${overwrite}`, yaml)).json()
    return { scenario: res.scenario, missing: res.missing || [] }
  },
}


export const api = {
  apps: () => call<Application[]>('GET', '/apps'),
  saveApp: (a: Application) => call<Application>('PUT', `/apps/${e(a.id)}`, a),
  deleteApp: (id: string) => call<void>('DELETE', `/apps/${e(id)}`),
  /** replace: update the application if it already exists. */
  importApp: (zip: ArrayBuffer, replace = false) => call<{ id: string }>('POST', `/import?replace=${replace}`, zip),
  exportApp: async (id: string) => (await send(current, 'GET', `/apps/${e(id)}/export`)).blob(),

  scenarios: (app: string) => call<Scenario[]>('GET', `/apps/${e(app)}/scenarios`),
  saveScenario: (app: string, s: Scenario) => call<Scenario>('PUT', `/apps/${e(app)}/scenarios/${e(s.id)}`, s),
  deleteScenario: (app: string, id: string) => call<void>('DELETE', `/apps/${e(app)}/scenarios/${e(id)}`),
  exportScenario: async (app: string, id: string) => (await send(current, 'GET', `/apps/${e(app)}/scenarios/${e(id)}/export`)).blob(),
  importScenario: (app: string, yaml: string, overwrite = false) =>
    call<{ scenario: string; created: string[]; kept: string[]; missing?: string[] }>('POST', `/apps/${e(app)}/scenarios/import?overwrite=${overwrite}`, yaml),
  describeGrpc: (app: string, req: { environment: string; auth?: string; mtls?: string; target: string; plaintext?: boolean; protoFiles?: string[] }) =>
    call<GrpcService[]>('POST', `/apps/${e(app)}/grpc/describe`, req),
  scenarioYaml: (app: string, id: string) => call<string>('GET', `/apps/${e(app)}/scenarios/${e(id)}/yaml`, undefined, true),
  saveScenarioYaml: (app: string, id: string, yaml: string) => call<Scenario>('PUT', `/apps/${e(app)}/scenarios/${e(id)}/yaml`, yaml),

  // Global: shared by every application.
  global: () => call<Global>('GET', '/global'),
  saveGlobal: (g: Global) => call<Global>('PUT', '/global', { environments: g.environments, regions: g.regions, defaultEnvironment: g.defaultEnvironment, values: g.values }),
  usage: () => call<Usage>('GET', '/global/usage'),
  auth: (env = '') => call<AuthProfile[]>('GET', `/global/auth?env=${e(env)}`),
  saveAuth: (p: AuthProfile) => call<AuthProfile>('PUT', `/global/auth/${e(p.id)}`, p),
  deleteAuth: (id: string) => call<void>('DELETE', `/global/auth/${e(id)}`),
  /** Tries a profile for real. app optionally fills its variables with that application's values. */
  checkAuth: (id: string, env: string, mtls = '', app = '') => call<AuthCheckResult>('POST', `/global/auth/${e(id)}/check?env=${e(env)}&mtls=${e(mtls)}&app=${e(app)}`),
  certSets: (env = '') => call<CertSet[]>('GET', `/global/tls?env=${e(env)}`),
  saveCertSet: (c: CertSet) => call<CertSet>('PUT', `/global/tls/${e(c.id)}`, c),
  deleteCertSet: (id: string) => call<void>('DELETE', `/global/tls/${e(id)}`),
  certs: () => call<Asset[]>('GET', '/global/certs'),
  /** replace: overwrite a file of that name; without it an existing file is refused (409). */
  uploadCert: (name: string, data: ArrayBuffer, replace = false) => call<void>('PUT', `/global/certs/${e(name)}?replace=${replace}`, data),
  deleteCert: (name: string) => call<void>('DELETE', `/global/certs/${e(name)}`),

  // An application's own .proto files.
  protos: (app: string) => call<Asset[]>('GET', `/apps/${e(app)}/protos`),
  uploadProto: (app: string, name: string, data: ArrayBuffer) => call<void>('PUT', `/apps/${e(app)}/protos/${e(name)}`, data),
  deleteProto: (app: string, name: string) => call<void>('DELETE', `/apps/${e(app)}/protos/${e(name)}`),

  run: (app: string, req: { environment?: string; scenarios?: string[]; kind?: string; until?: string }) => call<RunResult>('POST', `/apps/${e(app)}/run`, req),
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

/** base, or "base (2)", "base (3)", ...: the first name not taken. */
export function uniqueName(base: string, taken: string[]): string {
  if (!taken.includes(base)) return base
  for (let i = 2; ; i++) if (!taken.includes(`${base} (${i})`)) return `${base} (${i})`
}

export function when(iso: string): string {
  const d = new Date(iso)
  return isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url; link.download = name; link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** Turns a pasted curl command into the fields of an HTTP step. */
export function parseCurl(text: string): { method: string; url: string; headers?: Record<string, string>; body?: any } | null {
  const src = text.replace(/\\\r?\n/g, ' ').trim()
  if (!/^curl\b/i.test(src)) return null
  const words: string[] = []
  const re = /'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|(\S+)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src))) words.push(m[1] !== undefined ? m[1] : m[2] !== undefined ? m[2].replace(/\\(["\\$`])/g, '$1') : m[3])
  let method = '', url = '', data: string | undefined
  const headers: Record<string, string> = {}
  for (let i = 1; i < words.length; i++) {
    const w = words[i]
    if (w === '-X' || w === '--request') method = words[++i] || ''
    else if (w === '-H' || w === '--header') { const h = words[++i] || ''; const c = h.indexOf(':'); if (c > 0) headers[h.slice(0, c).trim()] = h.slice(c + 1).trim() }
    else if (['-d', '--data', '--data-raw', '--data-binary', '--data-ascii'].includes(w)) data = words[++i]
    else if (w === '--url') url = words[++i] || ''
    else if (['-u', '--user', '-o', '--output', '-A', '--user-agent', '-e', '--referer', '--connect-timeout', '-m', '--max-time', '--cert', '--key', '--cacert'].includes(w)) i++
    else if (!w.startsWith('-') && !url) url = w
  }
  if (!url) return null
  let body: any = data
  if (data !== undefined) { try { body = JSON.parse(data) } catch { /* keep as text */ } }
  return { method: (method || (data !== undefined ? 'POST' : 'GET')).toUpperCase(), url, headers: Object.keys(headers).length ? headers : undefined, body }
}
