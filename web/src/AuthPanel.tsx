import { useState } from 'react'
import { api, slug, uniqueId } from './api'
import type { Application, Asset, AuthCheckResult, AuthProfile, TLSSettings } from './types'
import { Badge, Check, Field, FileButton, KV, Notice, Select, Text, useAction } from './widgets'

const TYPES: [string, string][] = [
  ['none', 'None (TLS trust settings only)'],
  ['basic', 'Basic: username and password'],
  ['bearer', 'Bearer: fixed token'],
  ['api_key', 'API key in a header or query parameter'],
  ['oauth2_client_credentials', 'OAuth2: client credentials (fetches a token)'],
  ['oauth2_password', 'OAuth2: password grant (fetches a token)'],
  ['mtls', 'Mutual TLS: client certificate'],
]

const CERT_HELP = [
  { field: 'cert', label: 'Client certificate', required: true, kind: 'certificate', help: 'PEM certificate (plus any intermediates) issued to this test client by a CA the service trusts.' },
  { field: 'key', label: 'Client private key', required: true, kind: 'private-key', help: 'Unencrypted PEM private key that matches the client certificate.' },
  { field: 'ca', label: 'CA bundle', required: false, kind: 'certificate', help: 'PEM certificate(s) of the CA that signed the service’s own certificate. Only needed when that CA is private.' },
] as const

function ProfileEditor({ app, initial, isNew, assets, env, takenIds, onSaved, onDeleted, onUpload }: {
  app: Application; initial: AuthProfile; isNew: boolean; assets: Asset[]; env: string; takenIds: string[]
  onSaved: (p: AuthProfile) => void; onDeleted: () => void; onUpload: (f: File) => Promise<void>
}) {
  const [p, setP] = useState<AuthProfile>(initial)
  const [dirty, setDirty] = useState(isNew)
  const [check, setCheck] = useState<AuthCheckResult | null>(null)
  const act = useAction()
  const set = (patch: Partial<AuthProfile>) => { setP({ ...p, ...patch }); setDirty(true); setCheck(null) }
  const usesTls = p.type === 'mtls' || !!p.tls
  const tls = p.tls || {}
  const setTls = (patch: Partial<TLSSettings>) => {
    const next: any = { ...tls, ...patch }
    for (const k of Object.keys(next)) if (next[k] === '' || next[k] === false || next[k] === undefined) delete next[k]
    set({ tls: Object.keys(next).length || p.type === 'mtls' ? next : undefined })
  }
  const certs = assets.filter(a => a.category === 'certs')
  const o = p.oauth2 || { tokenUrl: '', clientId: '' }
  const setO = (patch: any) => set({ oauth2: { ...o, ...patch } })

  const save = async (): Promise<boolean> => {
    let ok = false
    await act.run(async () => {
      const id = isNew ? uniqueId(slug(p.name), takenIds) : p.id
      if (!slug(p.name) && isNew) throw new Error('Give the profile a name first.')
      const body: AuthProfile = { id, name: p.name, description: p.description, type: p.type, tls: p.tls }
      if (p.type === 'basic') body.basic = p.basic || { username: '', password: '' }
      if (p.type === 'bearer') body.bearer = p.bearer || { token: '' }
      if (p.type === 'api_key') body.apiKey = p.apiKey || { name: '', value: '' }
      if (p.type.startsWith('oauth2')) body.oauth2 = o
      const saved = await api.saveAuth(app.id, body)
      setP(saved); setDirty(false); onSaved(saved); ok = true
    })
    return ok
  }
  const test = async () => {
    if (dirty && !(await save())) return
    await act.run(async () => setCheck(await api.checkAuth(app.id, p.id, env)))
  }

  return (
    <div className="card">
      <div className="editor-bar">
        <div><h3>{isNew ? 'New auth profile' : p.name || p.id}</h3>{!isNew && <span className="muted mono">assets/auth/{p.id}.yaml</span>}</div>
        <div className="row">
          {!isNew && <button type="button" className="link danger" onClick={() => { if (confirm(`Delete auth profile “${p.name || p.id}”? Tests that use it will fail until they are changed.`)) act.run(async () => { await api.deleteAuth(app.id, p.id); onDeleted() }) }}>Delete</button>}
          <button type="button" onClick={test} disabled={act.busy}>Try it</button>
          <button type="button" className="primary" onClick={save} disabled={act.busy || !dirty}>{dirty ? 'Save' : 'Saved'}</button>
        </div>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
      {check && (check.ok
        ? <Notice kind="ok">Works. {check.sends?.length ? `Sends ${check.sends.join(', ')}.` : 'Sends no headers.'} {check.clientCert ? 'Presents the client certificate.' : ''}</Notice>
        : <Notice kind="error">Does not work yet: {check.error}</Notice>)}

      <div className="grid">
        <Field label="Name" wide><Text value={p.name} onChange={name => set({ name })} placeholder="Orders service client" /></Field>
        <Field label="Type" wide><Select value={p.type} onChange={type => set({ type, tls: type === 'mtls' ? (p.tls || {}) : p.tls })} options={TYPES} /></Field>
      </div>
      <Field label="Description" wide><Text value={p.description} onChange={description => set({ description: description || undefined })} /></Field>
      <p className="hint">Values can use variables, for example <code>{'{{baseUrl}}/oauth/token'}</code>. Keep secrets out of the file with <code>{'{{$env.MY_SECRET}}'}</code>, which reads an environment variable of the runner.</p>

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
          <div className="grid">
            <Field label="Token URL" wide><Text mono value={o.tokenUrl} onChange={tokenUrl => setO({ tokenUrl })} placeholder="{{baseUrl}}/oauth/token" /></Field>
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

      {p.type !== 'mtls' && <Check label="This profile also needs TLS settings (client certificate or a private CA)" value={usesTls} onChange={v => set({ tls: v ? {} : undefined })} />}
      {usesTls && (
        <div className="tls">
          <h4>{p.type === 'mtls' ? 'Certificates to provide for mutual TLS' : 'TLS settings'}</h4>
          <p className="hint">Files live in this application’s <code>assets/certs</code> folder. Pick an uploaded file or upload one here.</p>
          {CERT_HELP.map(c => {
            const required = c.required && (p.type === 'mtls' || !!tls.cert || !!tls.key)
            const chosen = (tls as any)[c.field] as string | undefined
            const saved = !dirty ? p.certChecks?.find(k => k.field === 'tls.' + c.field) : undefined
            const asset = certs.find(a => a.name === chosen)
            const wrongKind = asset && asset.kind !== c.kind
            return (
              <div className="cert-row" key={c.field}>
                <div>
                  <strong>{c.label}</strong> {required ? <Badge kind="k-negative">required</Badge> : <Badge kind="neutral">optional</Badge>}
                  <span className="hint">{c.help}</span>
                </div>
                <div className="row">
                  <Select value={chosen || ''} onChange={v => setTls({ [c.field]: v } as any)}
                    options={[['', '(none)'], ...certs.map(a => [a.name, `${a.name} · ${a.kind}`] as [string, string]), ...(chosen && !asset ? [[chosen, `${chosen} · missing`] as [string, string]] : [])]} />
                  <FileButton label="Upload…" accept=".pem,.crt,.cer,.key" onFile={async f => { await onUpload(f); setTls({ [c.field]: f.name } as any) }} />
                </div>
                <div>
                  {!chosen ? (required ? <span className="error-text">Not chosen yet.</span> : null)
                    : !asset ? <span className="error-text">{chosen} is not uploaded.</span>
                    : wrongKind ? <span className="error-text">{chosen} is a {asset.kind}, expected a {c.kind}.</span>
                    : asset.expired ? <span className="error-text">Expired on {asset.notAfter?.slice(0, 10)}.</span>
                    : saved && saved.status !== 'ok' ? <span className="error-text">{saved.status}: {saved.detail}</span>
                    : <span className="ok-text">✓ {asset.subject ? `${asset.subject}, valid until ${asset.notAfter?.slice(0, 10)}` : 'ready'}</span>}
                </div>
              </div>
            )
          })}
          <div className="grid">
            <Field label="Server name override" hint="When the certificate name differs from the host you call."><Text mono value={tls.serverName} onChange={serverName => setTls({ serverName })} /></Field>
          </div>
          <Check label="Skip server certificate verification (test environments only)" value={tls.insecureSkipVerify} onChange={insecureSkipVerify => setTls({ insecureSkipVerify })} />
        </div>
      )}
    </div>
  )
}

export function AuthPanel({ app, profiles, assets, env, reload }: { app: Application; profiles: AuthProfile[]; assets: Asset[]; env: string; reload: () => Promise<void> }) {
  const [selected, setSelected] = useState<string | null>(profiles[0]?.id ?? null)
  const [creating, setCreating] = useState(false)
  const act = useAction()
  const upload = (category: string) => async (f: File) => {
    await act.run(async () => { await api.uploadAsset(app.id, category, f.name, await f.arrayBuffer()); await reload() })
  }
  const current = profiles.find(p => p.id === selected)
  const status = (p: AuthProfile) => {
    if (p.description?.startsWith('INVALID: ')) return <Badge kind="st-failed">invalid file</Badge>
    const bad = (p.certChecks || []).filter(c => c.status !== 'ok' && (c.required || c.status !== 'not_set'))
    return bad.length ? <Badge kind="st-failed">{bad.length} cert issue{bad.length === 1 ? '' : 's'}</Badge> : null
  }
  return (
    <div className="split">
      <div className="list">
        <div className="list-head"><h3>Auth profiles</h3><button type="button" onClick={() => { setCreating(true); setSelected(null) }}>+ New</button></div>
        {profiles.length === 0 && !creating && <p className="muted">No profiles yet. Tests pick one of these to get a token, an API key or a client certificate.</p>}
        {profiles.map(p => (
          <button type="button" key={p.id} className={'list-item' + (selected === p.id ? ' on' : '')} onClick={() => { setSelected(p.id); setCreating(false) }}>
            <span className="result-name">{p.name || p.id}</span>
            <span className="muted">{p.type}{app.defaultAuth === p.id ? ' · default' : ''}</span>
            {status(p)}
          </button>
        ))}

        <div className="list-head"><h3>Certificates and files</h3></div>
        <p className="hint">Stored next to the application under <code>assets/</code>. Key contents are never shown.</p>
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
            <button type="button" className="icon" title={`Delete ${a.name}`} onClick={() => { if (confirm(`Delete ${a.name}? Profiles that use it will stop working.`)) act.run(async () => { await api.deleteAsset(app.id, a.category, a.name); await reload() }) }}>×</button>
          </div>
        ))}
        {assets.length === 0 && <p className="muted">Nothing uploaded yet.</p>}
      </div>
      <div className="detail">
        {creating && <ProfileEditor key="new" app={app} isNew initial={{ id: '', name: '', type: 'oauth2_client_credentials' }} assets={assets} env={env} takenIds={profiles.map(p => p.id)}
          onUpload={upload('certs')} onDeleted={() => setCreating(false)} onSaved={async p => { await reload(); setCreating(false); setSelected(p.id) }} />}
        {!creating && current && (current.description?.startsWith('INVALID: ')
          ? <Notice kind="error">assets/auth/{current.id}.yaml does not parse: {current.description.slice(9)}</Notice>
          : <ProfileEditor key={current.id} app={app} isNew={false} initial={current} assets={assets} env={env} takenIds={[]}
              onUpload={upload('certs')} onSaved={() => reload()} onDeleted={async () => { await reload(); setSelected(null) }} />)}
        {!creating && !current && <p className="muted">Select a profile, or create one.</p>}
      </div>
    </div>
  )
}
