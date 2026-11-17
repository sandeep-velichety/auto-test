import type { Application, Asset, AuthCheckResult, AuthProfile, RunResult, Scenario } from './types'

async function call<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const init: RequestInit = { method }
  if (body !== undefined) {
    if (typeof body === 'string' || body instanceof Blob || body instanceof ArrayBuffer) init.body = body as BodyInit
    else {
      init.body = JSON.stringify(body)
      init.headers = { 'Content-Type': 'application/json' }
    }
  }
  const res = await fetch('/api' + path, init)
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try { msg = (await res.json()).error || msg } catch { /* not JSON */ }
    throw new Error(msg)
  }
  if (res.status === 204) return undefined as T
  return (raw ? res.text() : res.json()) as Promise<T>
}

const e = encodeURIComponent

export const api = {
  apps: () => call<Application[]>('GET', '/apps'),
  saveApp: (a: Application) => call<Application>('PUT', `/apps/${e(a.id)}`, a),
  deleteApp: (id: string) => call<void>('DELETE', `/apps/${e(id)}`),
  importApp: (zip: ArrayBuffer) => call<{ id: string }>('POST', '/import', zip),

  scenarios: (app: string) => call<Scenario[]>('GET', `/apps/${e(app)}/scenarios`),
  saveScenario: (app: string, s: Scenario) => call<Scenario>('PUT', `/apps/${e(app)}/scenarios/${e(s.id)}`, s),
  deleteScenario: (app: string, id: string) => call<void>('DELETE', `/apps/${e(app)}/scenarios/${e(id)}`),
  scenarioYaml: (app: string, id: string) => call<string>('GET', `/apps/${e(app)}/scenarios/${e(id)}/yaml`, undefined, true),
  saveScenarioYaml: (app: string, id: string, yaml: string) => call<Scenario>('PUT', `/apps/${e(app)}/scenarios/${e(id)}/yaml`, yaml),

  auth: (app: string) => call<AuthProfile[]>('GET', `/apps/${e(app)}/auth`),
  saveAuth: (app: string, p: AuthProfile) => call<AuthProfile>('PUT', `/apps/${e(app)}/auth/${e(p.id)}`, p),
  deleteAuth: (app: string, id: string) => call<void>('DELETE', `/apps/${e(app)}/auth/${e(id)}`),
  checkAuth: (app: string, id: string, env: string) => call<AuthCheckResult>('POST', `/apps/${e(app)}/auth/${e(id)}/check?env=${e(env)}`),

  assets: (app: string) => call<Asset[]>('GET', `/apps/${e(app)}/assets`),
  uploadAsset: (app: string, category: string, name: string, data: ArrayBuffer) => call<void>('PUT', `/apps/${e(app)}/assets/${e(category)}/${e(name)}`, data),
  deleteAsset: (app: string, category: string, name: string) => call<void>('DELETE', `/apps/${e(app)}/assets/${e(category)}/${e(name)}`),

  run: (app: string, req: { environment?: string; scenarios?: string[]; kind?: string }) => call<RunResult>('POST', `/apps/${e(app)}/run`, req),
  runs: (app: string) => call<RunResult[]>('GET', `/apps/${e(app)}/runs`),
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
