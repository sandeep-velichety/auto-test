import { useState } from 'react'
import type { ReactNode } from 'react'
import { api, slug, uniqueId } from './api'
import type { Application, Asset, AuthCheckResult, AuthProfile, CertSet, TLSSettings } from './types'
import { Badge, Check, Field, FileButton, JsonField, KV, Notice, Select, Text, useAction } from './widgets'

const TYPES: [string, string][] = [
  ['oauth2_client_credentials', 'Token from an identity provider: OAuth2 client credentials (Okta, ForgeRock, …)'],
  ['oauth2_password', 'Token from an identity provider: OAuth2 password grant'],
  ['http_call', 'Token from a custom HTTP call (you define the request)'],
  ['headers', 'Fixed headers / keys sent with every request'],
  ['api_key', 'API key in a header or query parameter'],
  ['bearer', 'Bearer: fixed token'],
  ['basic', 'Basic: username and password'],
  ['none', 'None'],
]

/** Starting points for common identity providers. Addresses are patterns to adjust. */
const PRESETS: Record<string, { label: string; note: string; oauth2: NonNullable<AuthProfile['oauth2']> }> = {
  okta: {
    label: 'Okta',
    note: 'Replace the domain and the authorization server id (“default” is Okta’s built-in one). Okta needs at least one scope.',
    oauth2: { tokenUrl: 'https://{{oktaDomain}}/oauth2/default/v1/token', clientId: '', clientSecret: '{{$env.OKTA_CLIENT_SECRET}}', scopes: ['api.read'], clientAuth: 'basic' },
  },
  forgerock: {
    label: 'ForgeRock',
    note: 'Replace the host and realm. The path is the Access Management token endpoint; some installations use /openam instead of /am.',
    oauth2: { tokenUrl: 'https://{{forgerockHost}}/am/oauth2/realms/root/realms/{{forgerockRealm}}/access_token', clientId: '', clientSecret: '{{$env.FORGEROCK_CLIENT_SECRET}}', scopes: [] },
  },
}

const CERT_ROWS = [
  { field: 'cert', label: 'Client certificate', kind: 'certificate', help: 'PEM certificate (plus any intermediates) issued to this test client by a CA the service trusts.' },
  { field: 'key', label: 'Client private key', kind: 'private-key', help: 'Unencrypted PEM private key that matches the client certificate.' },
  { field: 'ca', label: 'CA bundle', kind: 'certificate', help: 'PEM certificate(s) of the CA that signed the service’s own certificate. Only needed when that CA is private.' },
] as const

/** Tabs for "all environments" and each environment that can differ. */
function EnvTabs({ envs, tab, onTab, overridden }: { envs: string[]; tab: string; onTab: (t: string) => void; overridden: (env: string) => boolean }) {
  if (envs.length === 0) return null
  return (
    <div className="envtabs" role="tablist" aria-label="Environment">
      <button type="button" role="tab" aria-selected={tab === ''} className={tab === '' ? 'on' : ''} onClick={() => onTab('')}>All environments</button>
      {envs.map(e => (
        <button type="button" role="tab" aria-selected={tab === e} key={e} className={tab === e ? 'on' : ''} onClick={() => onTab(e)}>
          {e}{overridden(e) ? ' •' : ''}
        </button>
      ))}
    </div>
  )
}

/** The three file pickers shared by certificate sets and profile TLS settings. */
function CertFiles({ value, fallback, clientRequired, certs, onChange, onUpload }: {
  value: TLSSettings; fallback?: TLSSettings; clientRequired: boolean; certs: Asset[]
  onChange: (patch: Partial<TLSSettings>) => void; onUpload: (f: File) => Promise<void>
}) {
  return (
    <>
      {CERT_ROWS.map(c => {
        const own = (value as any)[c.field] as string | undefined
        const inherited = fallback ? ((fallback as any)[c.field] as string | undefined) : undefined
        const chosen = own || inherited
        const required = c.field !== 'ca' && (clientRequired || !!value.cert || !!value.key || !!fallback?.cert)
        const asset = certs.find(a => a.name === chosen)
        const wrongKind = asset && asset.kind !== c.kind
        return (
          <div className="cert-row" key={c.field}>
            <div>
              <strong>{c.label}</strong> {required ? <Badge kind="k-negative">required</Badge> : <Badge kind="neutral">optional</Badge>}
              <span className="hint">{c.help}</span>
            </div>
            <div className="row">
              <Select value={own || ''} onChange={v => onChange({ [c.field]: v || undefined } as any)}
                options={[['', fallback ? `same as all environments${inherited ? ` (${inherited})` : ' (none)'}` : '(none)'],
                  ...certs.map(a => [a.name, `${a.name} · ${a.kind}`] as [string, string]),
                  ...(own && !certs.some(a => a.name === own) ? [[own, `${own} · missing`] as [string, string]] : [])]} />
              <FileButton label="Upload…" accept=".pem,.crt,.cer,.key" onFile={async f => { await onUpload(f); onChange({ [c.field]: f.name } as any) }} />
            </div>
            <div>
              {!chosen ? (required ? <span className="error-text">Not chosen yet.</span> : null)
                : !asset ? <span className="error-text">{chosen} is not uploaded.</span>
                : wrongKind ? <span className="error-text">{chosen} is a {asset.kind}, expected a {c.kind}.</span>
                : asset.expired ? <span className="error-text">Expired on {asset.notAfter?.slice(0, 10)}.</span>
                : <span className="ok-text">✓ {asset.subject ? `${asset.subject}, valid until ${asset.notAfter?.slice(0, 10)}` : 'ready'}</span>}
            </div>
          </div>
        )
      })}
      <div className="grid">
        <Field label="Server name override" hint="When the certificate name differs from the host you call.">
          <Text mono value={value.serverName} onChange={serverName => onChange({ serverName: serverName || undefined })} placeholder={fallback?.serverName} />
        </Field>
      </div>
      <Check label="Skip server certificate verification (test environments only)" value={value.insecureSkipVerify} onChange={v => onChange({ insecureSkipVerify: v || undefined })} />
    </>
  )
}

function cleanTLS(t: TLSSettings): TLSSettings {
  const out: any = {}
  for (const [k, v] of Object.entries(t)) if (v !== undefined && v !== '' && v !== false) out[k] = v
  return out
}

function CertSetEditor({ app, initial, isNew, assets, envs, takenIds, onSaved, onDeleted, onUpload }: {
  app: Application; initial: CertSet; isNew: boolean; assets: Asset[]; envs: string[]; takenIds: string[]
  onSaved: (c: CertSet) => void; onDeleted: () => void; onUpload: (f: File) => Promise<void>
}) {
  const [c, setC] = useState<CertSet>(initial)
  const [tab, setTab] = useState('')
  const [dirty, setDirty] = useState(isNew)
  const act = useAction()
  const certs = assets.filter(a => a.category === 'certs')
  const base: TLSSettings = { cert: c.cert, key: c.key, ca: c.ca, serverName: c.serverName, insecureSkipVerify: c.insecureSkipVerify }
  const override = tab ? c.environments?.[tab] || {} : undefined
  const change = (patch: Partial<TLSSettings>) => {
    setDirty(true)
    if (!tab) { setC({ ...c, ...patch }); return }
    const next = cleanTLS({ ...override, ...patch })
    const envsNext = { ...(c.environments || {}) }
    if (Object.keys(next).length) envsNext[tab] = next; else delete envsNext[tab]
    setC({ ...c, environments: Object.keys(envsNext).length ? envsNext : undefined })
  }
  const save = () => act.run(async () => {
    if (isNew && !slug(c.name)) throw new Error('Give the certificate set a name first.')
    const id = isNew ? uniqueId(slug(c.name), takenIds) : c.id
    const saved = await api.saveCertSet(app.id, { id, name: c.name, description: c.description, hosts: c.hosts?.length ? c.hosts : undefined, ...cleanTLS(base), environments: c.environments })
    setC(saved); setDirty(false); onSaved(saved)
  })
  return (
    <div className="card">
      <div className="editor-bar">
        <div><h3>{isNew ? 'New certificate set' : c.name || c.id}</h3>{!isNew && <span className="muted mono">assets/tls/{c.id}.yaml</span>}</div>
        <div className="row">
          {!isNew && <button type="button" className="link danger" onClick={() => { if (confirm(`Delete certificate set “${c.name || c.id}”? Tests that use it will fail until they are changed. The files stay.`)) act.run(async () => { await api.deleteCertSet(app.id, c.id); onDeleted() }) }}>Delete</button>}
          <button type="button" className="primary" onClick={save} disabled={act.busy || !dirty}>{dirty ? 'Save' : 'Saved'}</button>
        </div>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
      <div className="grid">
        <Field label="Name" wide><Text value={c.name} onChange={name => { setC({ ...c, name }); setDirty(true) }} placeholder="Orders client certificate" /></Field>
      </div>
      <Field label="Description" wide><Text value={c.description} onChange={description => { setC({ ...c, description: description || undefined }); setDirty(true) }} /></Field>
      <Field label="Use automatically for these hosts" wide hint="Comma separated, like client certificates in Postman: host, host:port or *.example.com. Variables such as {{ordersHost}} work, so the host can differ per environment. Leave empty to use this set only where a test picks it.">
        <Text mono value={(c.hosts || []).join(', ')} onChange={v => { setC({ ...c, hosts: v.split(',').map(h => h.trim()).filter(Boolean) }); setDirty(true) }} placeholder="orders.staging.example.com, *.internal.example.com:8443" />
      </Field>
      <p className="hint">A certificate set is what a call presents when mutual TLS is needed. It is used automatically for the hosts above, or wherever a test picks it under “Mutual TLS”. Files live in this application’s <code>assets/certs</code> folder.</p>
      <EnvTabs envs={envs} tab={tab} onTab={setTab} overridden={e => !!c.environments?.[e]} />
      {tab && (
        <p className="hint">
          Only what differs in <strong>{tab}</strong>. Anything left as “same as all environments” uses the shared value.{' '}
          {c.environments?.[tab] && <button type="button" className="link" onClick={() => change({ cert: undefined, key: undefined, ca: undefined, serverName: undefined, insecureSkipVerify: undefined })}>Reset {tab} to the shared values</button>}
        </p>
      )}
      <div className="tls" key={tab}>
        <CertFiles value={tab ? override! : base} fallback={tab ? base : undefined} clientRequired={false} certs={certs} onChange={change} onUpload={onUpload} />
      </div>
    </div>
  )
}

function ProfileEditor({ app, initial, isNew, assets, sets, envs, env, takenIds, onSaved, onDeleted, onUpload }: {
  app: Application; initial: AuthProfile; isNew: boolean; assets: Asset[]; sets: CertSet[]; envs: string[]; env: string; takenIds: string[]
  onSaved: (p: AuthProfile) => void; onDeleted: () => void; onUpload: (f: File) => Promise<void>
}) {
  const [stored, setStored] = useState<AuthProfile>(initial)
  const [tab, setTab] = useState('')
  const [dirty, setDirty] = useState(isNew)
  const [check, setCheck] = useState<AuthCheckResult | null>(null)
  const [tryWith, setTryWith] = useState('')
  const [preset, setPreset] = useState('')
  const act = useAction()

  // In an environment tab the form shows the merged result and edits go
  // into that environment's override, one block (oauth2, headers, …) at a time.
  const override = tab ? stored.environments?.[tab] || {} : {}
  const p: AuthProfile = tab ? { ...stored, ...override, id: stored.id, name: stored.name, description: stored.description, environments: stored.environments } : stored
  const set = (patch: Partial<AuthProfile>) => {
    setDirty(true); setCheck(null)
    if (!tab) { setStored({ ...stored, ...patch }); return }
    const next: Record<string, any> = { ...override, ...patch }
    for (const k of Object.keys(next)) if (next[k] === undefined) delete next[k]
    const all = { ...(stored.environments || {}) }
    if (Object.keys(next).length) all[tab] = next; else delete all[tab]
    setStored({ ...stored, environments: Object.keys(all).length ? all : undefined })
  }
  const resetEnv = () => {
    const all = { ...(stored.environments || {}) }
    delete all[tab]
    setStored({ ...stored, environments: Object.keys(all).length ? all : undefined }); setDirty(true); setCheck(null)
  }
  const changed = (block: string) => !!tab && block in override
  const mark = (block: string): ReactNode => (changed(block) ? <Badge kind="k-negative">differs in {tab}</Badge> : null)

  const o = p.oauth2 || { tokenUrl: '', clientId: '' }
  const setO = (patch: any) => set({ oauth2: { ...o, ...patch } })
  const h = p.httpCall || { url: '' }
  const setH = (patch: any) => set({ httpCall: { ...h, ...patch } })
  const hasOwnTLS = p.type === 'mtls' || !!p.tls
  const tls = p.tls || {}
  const certs = assets.filter(a => a.category === 'certs')
  const types: [string, string][] = p.type === 'mtls' ? [...TYPES, ['mtls', 'Mutual TLS only (older style; prefer a certificate set)']] : TYPES

  const body = (): AuthProfile => {
    const blocks = (src: Record<string, any>, type: string) => {
      const out: Record<string, any> = {}
      if (type === 'basic' && src.basic) out.basic = src.basic
      if (type === 'bearer' && src.bearer) out.bearer = src.bearer
      if (type === 'api_key' && src.apiKey) out.apiKey = src.apiKey
      if (type.startsWith('oauth2') && src.oauth2) out.oauth2 = src.oauth2
      if (type === 'http_call' && src.httpCall) out.httpCall = src.httpCall
      if (src.headers) out.headers = src.headers
      if (src.tls) out.tls = src.tls
      return out
    }
    const environments: Record<string, Record<string, any>> = {}
    for (const [name, ov] of Object.entries(stored.environments || {})) {
      const type = (ov.type as string) || stored.type
      const kept = { ...(ov.type ? { type: ov.type } : {}), ...blocks(ov, type) }
      if (Object.keys(kept).length) environments[name] = kept
    }
    return {
      id: isNew ? uniqueId(slug(stored.name), takenIds) : stored.id, name: stored.name, description: stored.description, type: stored.type,
      ...blocks(stored as any, stored.type), environments: Object.keys(environments).length ? environments : undefined,
    }
  }
  const save = async (): Promise<boolean> => {
    let ok = false
    await act.run(async () => {
      if (isNew && !slug(stored.name)) throw new Error('Give the profile a name first.')
      const saved = await api.saveAuth(app.id, body())
      setStored(saved); setDirty(false); onSaved(saved); ok = true
    })
    return ok
  }
  const test = async () => {
    if (dirty && !(await save())) return
    await act.run(async () => setCheck(await api.checkAuth(app.id, stored.id || body().id, tab || env, tryWith)))
  }

  return (
    <div className="card">
      <div className="editor-bar">
        <div><h3>{isNew ? 'New auth profile' : stored.name || stored.id}</h3>{!isNew && <span className="muted mono">assets/auth/{stored.id}.yaml</span>}</div>
        <div className="row">
          {!isNew && <button type="button" className="link danger" onClick={() => { if (confirm(`Delete auth profile “${stored.name || stored.id}”? Tests that use it will fail until they are changed.`)) act.run(async () => { await api.deleteAuth(app.id, stored.id); onDeleted() }) }}>Delete</button>}
          <button type="button" className="primary" onClick={save} disabled={act.busy || !dirty}>{dirty ? 'Save' : 'Saved'}</button>
        </div>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}

      <div className="grid">
        <Field label="Name" wide><Text value={stored.name} onChange={name => { setStored({ ...stored, name }); setDirty(true) }} placeholder="Orders service client" /></Field>
      </div>
      <Field label="Description" wide><Text value={stored.description} onChange={description => { setStored({ ...stored, description: description || undefined }); setDirty(true) }} /></Field>

      <EnvTabs envs={envs} tab={tab} onTab={t => { setTab(t); setCheck(null) }} overridden={e => !!stored.environments?.[e]} />
      {tab && (
        <p className="hint">
          Showing how this profile reads in <strong>{tab}</strong>. Anything you change here applies to {tab} only.{' '}
          {stored.environments?.[tab] && <button type="button" className="link" onClick={resetEnv}>Reset {tab} to the shared values</button>}
        </p>
      )}

      <div key={tab + (stored.environments?.[tab] ? '1' : '0') + preset}>
        <Field label="How this application authenticates" wide>
          <Select value={p.type} onChange={type => set({ type })} options={types} />
        </Field>
        {mark('type')}
        <p className="hint">Values can use variables such as <code>{'{{baseUrl}}'}</code>. Keep secrets out of the file with <code>{'{{$env.MY_SECRET}}'}</code>, which reads an environment variable of the backend. A secret saved here can be used and replaced but not read back: it shows as ********.</p>

        {p.type === 'basic' && (
          <div className="grid">
            <Field label="Username"><Text value={p.basic?.username} onChange={username => set({ basic: { password: '', ...p.basic, username } })} /></Field>
            <Field label="Password"><Text type="password" value={p.basic?.password} onChange={password => set({ basic: { username: '', ...p.basic, password } })} /></Field>
          </div>
        )}
        {p.type === 'bearer' && (
          <div className="grid">
            <Field label="Token" wide><Text mono value={p.bearer?.token} onChange={token => set({ bearer: { ...p.bearer, token } })} /></Field>
            <Field label="Prefix" hint='Default "Bearer". Use "none" to send the bare token.'><Text value={p.bearer?.prefix} onChange={prefix => set({ bearer: { token: '', ...p.bearer, prefix: prefix || undefined } })} placeholder="Bearer" /></Field>
          </div>
        )}
        {p.type === 'api_key' && (
          <div className="grid">
            <Field label="Name"><Text mono value={p.apiKey?.name} onChange={name => set({ apiKey: { value: '', ...p.apiKey, name } })} placeholder="X-Api-Key" /></Field>
            <Field label="Value"><Text mono value={p.apiKey?.value} onChange={value => set({ apiKey: { name: '', ...p.apiKey, value } })} /></Field>
            <Field label="Sent in"><Select value={p.apiKey?.in || 'header'} onChange={v => set({ apiKey: { name: '', value: '', ...p.apiKey, in: v === 'header' ? undefined : v } })} options={['header', 'query']} /></Field>
          </div>
        )}
        {p.type.startsWith('oauth2') && (
          <>
            <div className="row">
              <span className="muted">Start from:</span>
              {Object.entries(PRESETS).map(([id, pr]) => (
                <button type="button" key={id} onClick={() => { set({ type: p.type, oauth2: { ...pr.oauth2, clientId: o.clientId, username: o.username, password: o.password } }); setPreset(id + Date.now()) }}>{pr.label}</button>
              ))}
              {mark('oauth2')}
            </div>
            {preset && PRESETS[preset.replace(/\d+$/, '')] && <Notice kind="info">{PRESETS[preset.replace(/\d+$/, '')].note}</Notice>}
            <p className="hint">The backend asks the identity provider for a token before the first test that needs it, then sends it as a bearer token on every request.</p>
            <div className="grid">
              <Field label="Token URL" wide><Text mono value={o.tokenUrl} onChange={tokenUrl => setO({ tokenUrl })} placeholder="https://idp.example.com/oauth2/token" /></Field>
              <Field label="Client id"><Text mono value={o.clientId} onChange={clientId => setO({ clientId })} /></Field>
              <Field label="Client secret"><Text mono value={o.clientSecret} onChange={clientSecret => setO({ clientSecret: clientSecret || undefined })} placeholder="{{$env.CLIENT_SECRET}}" /></Field>
              {p.type === 'oauth2_password' && <Field label="Username"><Text value={o.username} onChange={username => setO({ username })} /></Field>}
              {p.type === 'oauth2_password' && <Field label="Password"><Text type="password" value={o.password} onChange={password => setO({ password })} /></Field>}
              <Field label="Scopes" hint="Space separated"><Text mono value={(o.scopes || []).join(' ')} onChange={v => setO({ scopes: v.split(/\s+/).filter(Boolean) })} /></Field>
              <Field label="Audience"><Text mono value={o.audience} onChange={audience => setO({ audience: audience || undefined })} /></Field>
              <Field label="Client credentials sent"><Select value={o.clientAuth || 'body'} onChange={v => setO({ clientAuth: v === 'body' ? undefined : v })} options={[['body', 'in the form body'], ['basic', 'as HTTP Basic']]} /></Field>
              <Field label="Token field in the response"><Text mono value={o.tokenField} onChange={tokenField => setO({ tokenField: tokenField || undefined })} placeholder="access_token" /></Field>
              <Field label="Header to send the token in"><Text mono value={o.headerName} onChange={headerName => setO({ headerName: headerName || undefined })} placeholder="Authorization" /></Field>
              <Field label="Prefix"><Text value={o.prefix} onChange={prefix => setO({ prefix: prefix || undefined })} placeholder="Bearer" /></Field>
            </div>
            <Field group label="Extra form parameters" wide><KV value={o.extraParams} onChange={extraParams => setO({ extraParams })} /></Field>
          </>
        )}
        {p.type === 'http_call' && (
          <>
            <p className="hint">Describe the request that returns a token. The backend makes this call before the first test that needs it, reads the token from the response and sends it on every request. {mark('httpCall')}</p>
            <div className="grid">
              <Field label="Method"><Select value={h.method || 'POST'} onChange={method => setH({ method })} options={['POST', 'GET', 'PUT']} /></Field>
              <Field label="URL" wide><Text mono value={h.url} onChange={url => setH({ url })} placeholder="{{baseUrl}}/auth/login" /></Field>
            </div>
            <Field group label="Request headers" wide><KV value={h.headers} onChange={headers => setH({ headers })} keyLabel="header" addLabel="Add header" /></Field>
            <Field group label="Query parameters" wide><KV value={h.query} onChange={query => setH({ query })} addLabel="Add parameter" /></Field>
            <Field label="JSON body" wide hint="Use {{$env.NAME}} for passwords; the body is stored as written."><JsonField allowText rows={4} value={h.body} onChange={v => setH({ body: v })} placeholder='{"username": "qa", "password": "{{$env.QA_PASSWORD}}"}' /></Field>
            <Field group label="Or form fields (instead of a JSON body)" wide><KV value={h.form} onChange={form => setH({ form })} addLabel="Add field" /></Field>
            <div className="grid">
              <Field label="Where the token is in the response" hint="A path such as access_token or data.session.token, or $header.Name"><Text mono value={h.tokenPath} onChange={tokenPath => setH({ tokenPath: tokenPath || undefined })} placeholder="access_token" /></Field>
              <Field label="Header to send the token in"><Text mono value={h.headerName} onChange={headerName => setH({ headerName: headerName || undefined })} placeholder="Authorization" /></Field>
              <Field label="Prefix" hint='"none" sends the bare token'><Text value={h.prefix} onChange={prefix => setH({ prefix: prefix || undefined })} placeholder="Bearer" /></Field>
            </div>
          </>
        )}

        <Field group label={p.type === 'headers' ? 'Headers sent with every request' : 'Extra headers sent with every request (optional)'} wide
          hint="For example X-Api-Key, X-Tenant or a client id header. Values are write-only once saved.">
          <KV value={p.headers} onChange={headers => set({ headers })} keyLabel="header" addLabel="Add header" />
        </Field>
        {mark('headers')}

        {hasOwnTLS && (
          <div className="tls">
            <h4>Certificates stored on this profile (older style)</h4>
            <p className="hint">Certificate sets are now chosen on the test under “Mutual TLS”. These files are still used when a test picks no certificate set.{' '}
              {p.type !== 'mtls' && <button type="button" className="link" onClick={() => set({ tls: undefined })}>Remove them from this profile</button>}</p>
            <CertFiles value={tls} clientRequired={p.type === 'mtls'} certs={certs} onUpload={onUpload}
              onChange={patch => { const next = cleanTLS({ ...tls, ...patch }); set({ tls: Object.keys(next).length || p.type === 'mtls' ? next : undefined }) }} />
          </div>
        )}
      </div>

      <div className="tryit">
        <div className="row">
          <strong>Try it in {tab || env || 'the default environment'}</strong>
          <label className="inline">with certificate set <Select value={tryWith} onChange={setTryWith} options={[['', 'none'], ...sets.map(s => [s.id, s.name || s.id] as [string, string])]} /></label>
          <button type="button" onClick={test} disabled={act.busy}>{act.busy ? 'Working…' : dirty ? 'Save and try' : 'Try it'}</button>
        </div>
        {check && (check.ok
          ? <Notice kind="ok">Works. {check.sends?.length ? `Sends ${check.sends.join(', ')}.` : 'Sends no headers.'} {check.clientCert ? 'Presents the client certificate.' : ''}</Notice>
          : <Notice kind="error">Does not work yet: {check.error}</Notice>)}
      </div>
    </div>
  )
}

type Selection = { kind: 'profile' | 'set'; id: string } | { kind: 'new-profile' | 'new-set' } | null

export function AuthPanel({ app, profiles, sets, assets, env, reload }: { app: Application; profiles: AuthProfile[]; sets: CertSet[]; assets: Asset[]; env: string; reload: () => Promise<void> }) {
  const [sel, setSel] = useState<Selection>(profiles[0] ? { kind: 'profile', id: profiles[0].id } : sets[0] ? { kind: 'set', id: sets[0].id } : null)
  const act = useAction()
  const envs = Object.keys(app.environments || {})
  const upload = (category: string) => async (f: File) => {
    await act.run(async () => { await api.uploadAsset(app.id, category, f.name, await f.arrayBuffer()); await reload() })
  }
  const profile = sel && sel.kind === 'profile' ? profiles.find(p => p.id === sel.id) : undefined
  const certSet = sel && sel.kind === 'set' ? sets.find(c => c.id === sel.id) : undefined
  const issues = (checks: AuthProfile['certChecks']) => (checks || []).filter(c => c.status !== 'ok' && (c.required || c.status !== 'not_set')).length
  const flag = (invalid: boolean, n: number) => invalid ? <Badge kind="st-failed">invalid file</Badge> : n ? <Badge kind="st-failed">{n} cert issue{n === 1 ? '' : 's'} in {env || 'default'}</Badge> : null
  const on = (kind: string, id: string) => (sel && 'id' in sel && sel.kind === kind && sel.id === id ? ' on' : '')

  return (
    <div className="split">
      <div className="list">
        <div className="list-head"><h3>Certificate sets (mutual TLS)</h3><button type="button" onClick={() => setSel({ kind: 'new-set' })}>+ New</button></div>
        {sets.length === 0 && <p className="muted">None yet. A certificate set names the client certificate, key and CA this application uses; tests then choose it under “Mutual TLS”.</p>}
        {sets.map(c => (
          <button type="button" key={c.id} className={'list-item' + on('set', c.id)} onClick={() => setSel({ kind: 'set', id: c.id })}>
            <span className="result-name">{c.name || c.id}</span>
            <span className="muted">{c.hosts?.length ? `for ${c.hosts.join(', ')}` : [c.cert, c.ca].filter(Boolean).join(' · ') || 'no files chosen'}{c.environments ? ` · differs in ${Object.keys(c.environments).join(', ')}` : ''}</span>
            {flag(!!c.description?.startsWith('INVALID: '), issues(c.checks))}
          </button>
        ))}

        <div className="list-head"><h3>Auth profiles</h3><button type="button" onClick={() => setSel({ kind: 'new-profile' })}>+ New</button></div>
        {profiles.length === 0 && <p className="muted">None yet. An auth profile is how a test gets its token, key or headers.</p>}
        {profiles.map(p => (
          <button type="button" key={p.id} className={'list-item' + on('profile', p.id)} onClick={() => setSel({ kind: 'profile', id: p.id })}>
            <span className="result-name">{p.name || p.id}</span>
            <span className="muted">{p.type}{app.defaultAuth === p.id ? ' · default' : ''}{p.environments ? ` · differs in ${Object.keys(p.environments).join(', ')}` : ''}</span>
            {flag(!!p.description?.startsWith('INVALID: '), issues(p.certChecks))}
          </button>
        ))}

        <div className="list-head"><h3>Certificate and key files</h3></div>
        <p className="hint">Stored with this application under <code>assets/</code>. Key contents are never shown.</p>
        <div className="row">
          <FileButton label="Upload certificate or key" accept=".pem,.crt,.cer,.key" onFile={upload('certs')} />
          <FileButton label="Upload .proto" accept=".proto" onFile={upload('protos')} />
        </div>
        {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
        {assets.map(a => (
          <div className="asset" key={a.category + a.name}>
            <div>
              <span className="mono">{a.name}</span> <Badge kind="neutral">{a.kind}</Badge> {a.expired && <Badge kind="st-failed">expired</Badge>}
              {a.subject && <span className="hint">{a.subject}{a.isCA ? ' (CA)' : ''} · until {a.notAfter?.slice(0, 10)}</span>}
            </div>
            <button type="button" className="icon" title={`Delete ${a.name}`} onClick={() => { if (confirm(`Delete ${a.name}? Anything that uses it will stop working.`)) act.run(async () => { await api.deleteAsset(app.id, a.category, a.name); await reload() }) }}>×</button>
          </div>
        ))}
        {assets.length === 0 && <p className="muted">Nothing uploaded yet.</p>}
      </div>

      <div className="detail">
        {sel?.kind === 'new-set' && <CertSetEditor key="new-set" app={app} isNew initial={{ id: '', name: '' }} assets={assets} envs={envs} takenIds={sets.map(c => c.id)}
          onUpload={upload('certs')} onDeleted={() => setSel(null)} onSaved={async c => { await reload(); setSel({ kind: 'set', id: c.id }) }} />}
        {certSet && (certSet.description?.startsWith('INVALID: ')
          ? <Notice kind="error">assets/tls/{certSet.id}.yaml does not parse: {certSet.description.slice(9)}</Notice>
          : <CertSetEditor key={'set-' + certSet.id} app={app} isNew={false} initial={certSet} assets={assets} envs={envs} takenIds={[]}
              onUpload={upload('certs')} onSaved={() => reload()} onDeleted={async () => { await reload(); setSel(null) }} />)}
        {sel?.kind === 'new-profile' && <ProfileEditor key="new-profile" app={app} isNew initial={{ id: '', name: '', type: 'oauth2_client_credentials' }} assets={assets} sets={sets} envs={envs} env={env} takenIds={profiles.map(p => p.id)}
          onUpload={upload('certs')} onDeleted={() => setSel(null)} onSaved={async p => { await reload(); setSel({ kind: 'profile', id: p.id }) }} />}
        {profile && (profile.description?.startsWith('INVALID: ')
          ? <Notice kind="error">assets/auth/{profile.id}.yaml does not parse: {profile.description.slice(9)}</Notice>
          : <ProfileEditor key={'profile-' + profile.id} app={app} isNew={false} initial={profile} assets={assets} sets={sets} envs={envs} env={env} takenIds={[]}
              onUpload={upload('certs')} onSaved={() => reload()} onDeleted={async () => { await reload(); setSel(null) }} />)}
        {!sel && <p className="muted">Select a certificate set or an auth profile, or create one.</p>}
      </div>
    </div>
  )
}
