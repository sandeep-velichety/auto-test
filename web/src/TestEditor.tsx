import type { AuthProfile, Assertion, Expect, Test, TestType, Variant } from './types'
import { uniqueId } from './api'
import { Badge, Check, Field, JsonField, KV, Num, Section, Select, Text } from './widgets'

const OPS = ['equals', 'not_equals', 'contains', 'not_contains', 'matches', 'exists', 'not_exists', 'gt', 'gte', 'lt', 'lte', 'length', 'in']
const GRPC_CODES = ['', 'OK', 'CANCELLED', 'UNKNOWN', 'INVALID_ARGUMENT', 'DEADLINE_EXCEEDED', 'NOT_FOUND', 'ALREADY_EXISTS', 'PERMISSION_DENIED',
  'RESOURCE_EXHAUSTED', 'FAILED_PRECONDITION', 'ABORTED', 'OUT_OF_RANGE', 'UNIMPLEMENTED', 'INTERNAL', 'UNAVAILABLE', 'DATA_LOSS', 'UNAUTHENTICATED']

export const TYPE_LABEL: Record<TestType, string> = { http: 'HTTP call', grpc: 'gRPC call', kafka: 'Kafka', mongo: 'Mongo check', postgres: 'Postgres check' }

export function blankStep(type: TestType): any {
  switch (type) {
    case 'http': return { method: 'GET', url: '{{baseUrl}}/' }
    case 'grpc': return { target: '{{grpcTarget}}', method: '' }
    case 'kafka': return { brokers: '{{kafkaBrokers}}', topic: '', action: 'consume' }
    case 'mongo': return { uri: '{{mongoUri}}', database: '', collection: '', operation: 'find' }
    case 'postgres': return { dsn: '{{postgresDsn}}', query: '' }
  }
}

function clean<T extends object>(o: T): T | undefined {
  const out: any = {}
  for (const [k, v] of Object.entries(o)) {
    if (v === undefined || v === '' || v === false || (Array.isArray(v) && v.length === 0)) continue
    out[k] = v
  }
  return Object.keys(out).length ? out : undefined
}

function valueText(v: any): string {
  if (v === undefined || v === null) return ''
  return typeof v === 'string' ? v : JSON.stringify(v)
}

/** "2" -> 2, "true" -> true, '["a"]' -> array; anything else stays text. */
function parseValue(t: string): any {
  if (t === '') return undefined
  if (/^-?\d+(\.\d+)?$/.test(t) || t === 'true' || t === 'false' || t === 'null' || /^[[{]/.test(t)) {
    try { return JSON.parse(t) } catch { return t }
  }
  return t
}

function Assertions({ value, onChange, addLabel }: { value: Assertion[] | undefined; onChange: (v: Assertion[] | undefined) => void; addLabel: string }) {
  const rows = value || []
  const set = (i: number, patch: Partial<Assertion>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  return (
    <div className="kv">
      {rows.map((a, i) => (
        <div className="kv-row three" key={i}>
          <input className="mono" value={a.path} placeholder="path, e.g. items.0.id" spellCheck={false} onChange={e => set(i, { path: e.target.value })} />
          <Select value={a.op || 'equals'} onChange={op => set(i, { op })} options={OPS} />
          <input className="mono" value={valueText(a.value)} placeholder={a.op === 'exists' || a.op === 'not_exists' ? '(no value)' : 'value'} spellCheck={false}
            disabled={a.op === 'exists' || a.op === 'not_exists'} onChange={e => set(i, { value: parseValue(e.target.value) })} />
          <button type="button" className="icon" title="Remove" onClick={() => { const n = rows.filter((_, j) => j !== i); onChange(n.length ? n : undefined) }}>×</button>
        </div>
      ))}
      <button type="button" className="link" onClick={() => onChange([...rows, { path: '', op: 'equals' }])}>+ {addLabel}</button>
    </div>
  )
}

export function ExpectEditor({ type, kind, value, onChange }: { type: TestType; kind: string; value: Expect | undefined; onChange: (v: Expect | undefined) => void }) {
  const x = value || {}
  const set = (patch: Partial<Expect>) => onChange(clean({ ...x, ...patch }))
  const negative = kind === 'negative'
  const fallback: Record<TestType, string> = {
    http: negative ? 'any status outside 2xx' : 'any 2xx status',
    grpc: negative ? 'any code except OK' : 'code OK',
    kafka: negative ? 'no matching message arrives' : 'a matching message arrives',
    mongo: negative ? 'no documents are returned' : 'at least one document is returned',
    postgres: negative ? 'no rows are returned' : 'at least one row is returned (or the statement runs)',
  }
  return (
    <div>
      <p className="hint">Leave the main check empty to use the default for a {negative ? 'negative' : 'positive'} test: {fallback[type]}.</p>
      <div className="grid">
        {type === 'http' && <Field label="Status"><Num value={x.status} onChange={status => set({ status })} placeholder={negative ? 'e.g. 400' : 'e.g. 200'} /></Field>}
        {type === 'grpc' && <Field label="gRPC code"><Select value={x.code || ''} onChange={code => set({ code })} options={GRPC_CODES.map(c => [c, c || '(default)'] as [string, string])} /></Field>}
        {(type === 'mongo' || type === 'postgres') && <Field label={type === 'mongo' ? 'Document count' : 'Row count'}><Num value={x.count} onChange={count => set({ count })} placeholder="exact count" /></Field>}
        {type === 'kafka' && (
          <Field label="Message">
            <Select value={x.found === undefined ? '' : String(x.found)} onChange={v => set({ found: v === '' ? undefined : v === 'true' })}
              options={[['', '(default)'], ['true', 'must arrive'], ['false', 'must not arrive']]} />
          </Field>
        )}
        <Field label="Max duration (ms)"><Num value={x.maxDurationMs} onChange={maxDurationMs => set({ maxDurationMs })} /></Field>
        <Field label="Call fails with error containing" hint="For calls that never get a response, such as a refused TLS handshake."><Text value={x.error} onChange={error => set({ error })} /></Field>
      </div>
      <Field group label="Checks on the result" wide hint={type === 'postgres' ? 'Result is {count, rows:[…]}: use rows.0.column' : type === 'mongo' ? 'Result is {count, documents:[…]}: use documents.0.field' : type === 'kafka' ? 'Result is {key, value, headers, partition, offset}: use value.field' : 'Paths into the JSON response, e.g. data.items.0.id'}>
        <Assertions value={x.body} onChange={body => set({ body })} addLabel="Add check" />
      </Field>
      {type === 'http' && <Field group label="Response headers equal" wide><KV value={x.headers} onChange={headers => set({ headers })} keyLabel="header" /></Field>}
    </div>
  )
}

function StepFields({ type, step, set, protos }: { type: TestType; step: any; set: (patch: any) => void; protos: string[] }) {
  const s = step || {}
  switch (type) {
    case 'http':
      return (
        <>
          <div className="grid">
            <Field label="Method"><Select value={s.method || 'GET'} onChange={method => set({ method })} options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']} /></Field>
            <Field label="URL" wide><Text mono value={s.url} onChange={url => set({ url })} placeholder="{{baseUrl}}/orders" /></Field>
            <Field label="Timeout (ms)"><Num value={s.timeoutMs} onChange={timeoutMs => set({ timeoutMs })} placeholder="30000" /></Field>
          </div>
          <Field group label="Headers" wide hint="Added on top of what the auth profile sends. An empty value removes a header."><KV value={s.headers} onChange={headers => set({ headers })} keyLabel="header" addLabel="Add header" /></Field>
          <Field group label="Query parameters" wide><KV value={s.query} onChange={query => set({ query })} addLabel="Add parameter" /></Field>
          <Field label="Body" wide hint="JSON, or any text."><JsonField allowText value={s.body} onChange={body => set({ body })} /></Field>
          <Check label="Follow redirects" value={s.followRedirects} onChange={followRedirects => set({ followRedirects })} />
        </>
      )
    case 'grpc':
      return (
        <>
          <div className="grid">
            <Field label="Target (host:port)"><Text mono value={s.target} onChange={target => set({ target })} placeholder="{{grpcTarget}}" /></Field>
            <Field label="Method" wide><Text mono value={s.method} onChange={method => set({ method })} placeholder="package.Service/Method" /></Field>
            <Field label="Timeout (ms)"><Num value={s.timeoutMs} onChange={timeoutMs => set({ timeoutMs })} placeholder="30000" /></Field>
          </div>
          <Check label="Plaintext (no TLS)" value={s.plaintext} onChange={plaintext => set({ plaintext })} />
          <Field label="Proto files" wide hint={protos.length ? 'Comma separated file names from this application’s protos. Leave empty to use server reflection.' : 'Empty = server reflection. Upload .proto files under Auth & assets to use them here.'}>
            <Text mono value={(s.protoFiles || []).join(', ')} onChange={v => set({ protoFiles: v.split(',').map((p: string) => p.trim()).filter(Boolean) })} placeholder={protos.join(', ')} />
          </Field>
          <Field group label="Metadata" wide><KV value={s.metadata} onChange={metadata => set({ metadata })} keyLabel="key" addLabel="Add metadata" /></Field>
          <Field label="Request message (JSON)" wide><JsonField value={s.message} onChange={message => set({ message })} /></Field>
        </>
      )
    case 'kafka':
      return (
        <>
          <div className="grid">
            <Field label="Action"><Select value={s.action || 'consume'} onChange={action => set({ action })} options={[['consume', 'wait for a message'], ['produce', 'publish a message']]} /></Field>
            <Field label="Brokers"><Text mono value={s.brokers} onChange={brokers => set({ brokers })} placeholder="host1:9092,host2:9092" /></Field>
            <Field label="Topic"><Text mono value={s.topic} onChange={topic => set({ topic })} /></Field>
            <Field label="Timeout (ms)"><Num value={s.timeoutMs} onChange={timeoutMs => set({ timeoutMs })} placeholder="10000" /></Field>
            <Field label="SASL" hint="Username and password come from a basic auth profile."><Select value={s.sasl || ''} onChange={sasl => set({ sasl })} options={[['', 'none'], 'plain', 'scram-sha-256', 'scram-sha-512']} /></Field>
          </div>
          <Check label="Use TLS" value={s.tls} onChange={tls => set({ tls })} />
          {(s.action || 'consume') === 'consume' ? (
            <>
              <Field label="Read from">
                <Select value={s.from || 'scenario_start'} onChange={from => set({ from: from === 'scenario_start' ? undefined : from })}
                  options={[['scenario_start', 'messages published since the scenario started'], ['earliest', 'the whole topic']]} />
              </Field>
              <Field group label="Message to wait for" wide hint="All conditions must hold. Paths: key, value.field, headers.name">
                <Assertions value={s.match} onChange={match => set({ match })} addLabel="Add condition" />
              </Field>
            </>
          ) : (
            <>
              <Field group label="Key"><Text mono value={s.key} onChange={key => set({ key })} /></Field>
              <Field group label="Headers" wide><KV value={s.headers} onChange={headers => set({ headers })} keyLabel="header" /></Field>
              <Field label="Value" wide hint="JSON, or any text."><JsonField allowText value={s.value} onChange={value => set({ value })} /></Field>
            </>
          )}
        </>
      )
    case 'mongo':
      return (
        <>
          <div className="grid">
            <Field label="Connection URI" wide><Text mono value={s.uri} onChange={uri => set({ uri })} placeholder="{{mongoUri}}" /></Field>
            <Field label="Database"><Text mono value={s.database} onChange={database => set({ database })} /></Field>
            <Field label="Collection"><Text mono value={s.collection} onChange={collection => set({ collection })} /></Field>
            <Field label="Operation"><Select value={s.operation || 'find'} onChange={operation => set({ operation })} options={['find', 'count', 'aggregate']} /></Field>
            <Field label="Limit"><Num value={s.limit} onChange={limit => set({ limit })} placeholder="100" /></Field>
          </div>
          {s.operation === 'aggregate'
            ? <Field label="Pipeline (JSON array)" wide><JsonField value={s.pipeline} onChange={pipeline => set({ pipeline })} placeholder='[{"$match": {"status": "created"}}]' /></Field>
            : <Field label="Filter (JSON)" wide hint='Extended JSON works: {"_id": {"$oid": "{{orderId}}"}}'><JsonField value={s.filter} onChange={filter => set({ filter })} placeholder='{"orderId": "{{orderId}}"}' /></Field>}
        </>
      )
    case 'postgres':
      return (
        <>
          <div className="grid">
            <Field label="Connection string (DSN)" wide><Text mono value={s.dsn} onChange={dsn => set({ dsn })} placeholder="{{postgresDsn}}" /></Field>
            <Field label="Timeout (ms)"><Num value={s.timeoutMs} onChange={timeoutMs => set({ timeoutMs })} placeholder="15000" /></Field>
          </div>
          <Field label="SQL" wide hint="Use $1, $2 … for arguments."><textarea className="mono" rows={4} value={s.query || ''} spellCheck={false} onChange={e => set({ query: e.target.value })} placeholder="SELECT status FROM orders WHERE id = $1" /></Field>
          <Field label="Arguments (JSON array)" wide><JsonField rows={2} value={s.args} onChange={args => set({ args })} placeholder='["{{orderId}}"]' /></Field>
        </>
      )
  }
}

export function AuthSelect({ value, onChange, profiles, inherited }: { value: string | undefined; onChange: (v: string | undefined) => void; profiles: AuthProfile[]; inherited: string }) {
  const effective = value || inherited
  const profile = profiles.find(p => p.id === effective)
  const checks = profile?.certChecks || []
  const unknown = !!effective && effective !== 'none' && !profile
  return (
    <div>
      <Select value={value || ''} onChange={v => onChange(v || undefined)}
        options={[['', `inherit (${inherited || 'none'})`], ['none', 'none: send no credentials'], ...profiles.map(p => [p.id, `${p.name || p.id} · ${p.type}`] as [string, string])]} />
      {unknown && <span className="hint error">Auth profile “{effective}” does not exist. Create it under Auth &amp; assets.</span>}
      {checks.length > 0 && (
        <ul className="certlist">
          {checks.filter(c => c.required || c.status !== 'not_set').map(c => (
            <li key={c.field} className={c.status === 'ok' ? 'ok' : 'bad'}>
              <strong>{c.label}</strong>{' '}
              {c.status === 'ok' ? <>ready: <code>{c.file}</code></> : c.status === 'not_set' ? 'not chosen yet' : <>{c.status.replace('_', ' ')}{c.file ? <>: <code>{c.file}</code></> : null}</>}
              {c.status !== 'ok' && <span className="hint">{c.help} Add it under Auth &amp; assets.</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function VariantEditor({ type, v, onChange, onRemove, profiles, inherited }: {
  type: TestType; v: Variant; onChange: (v: Variant) => void; onRemove: () => void; profiles: AuthProfile[]; inherited: string
}) {
  const set = (patch: Partial<Variant>) => onChange({ ...v, ...patch })
  const kind = v.kind || 'negative'
  return (
    <div className="variant">
      <div className="grid">
        <Field label="Case name" wide><Text value={v.name} onChange={name => set({ name })} placeholder="Rejected when quantity is zero" /></Field>
        <Field label="Kind"><Select value={kind} onChange={k => set({ kind: k === 'negative' ? undefined : k })} options={['negative', 'positive']} /></Field>
        <Field group label="Auth for this case"><AuthSelect value={v.auth} onChange={auth => set({ auth })} profiles={profiles} inherited={inherited} /></Field>
      </div>
      <Field label={`What changes compared with the parent (${type} fields, JSON)`} wide hint='Only the differences. Example: {"body": {"quantity": 0}} or {"headers": {"X-Tenant": ""}}. null removes a field.'>
        <JsonField rows={3} value={(v as any)[type]} onChange={o => set({ [type]: o && typeof o === 'object' ? o : undefined } as any)} placeholder="{}" />
      </Field>
      <ExpectEditor type={type} kind={kind} value={v.expect} onChange={expect => set({ expect })} />
      <div className="row">
        <Check label="Skip" value={v.skip} onChange={skip => set({ skip: skip || undefined })} />
        <button type="button" className="link danger" onClick={onRemove}>Remove case</button>
      </div>
    </div>
  )
}

export function TestEditor({ test, onChange, profiles, inheritedAuth, protos }: {
  test: Test; onChange: (t: Test) => void; profiles: AuthProfile[]; inheritedAuth: string; protos: string[]
}) {
  const set = (patch: Partial<Test>) => onChange({ ...test, ...patch })
  const kind = test.kind || 'positive'
  const variants = test.variants || []
  const setType = (type: TestType) => {
    const next: Test = { ...test, type, http: undefined, grpc: undefined, kafka: undefined, mongo: undefined, postgres: undefined, expect: undefined, variants: undefined }
    ;(next as any)[type] = blankStep(type)
    onChange(next)
  }
  const addVariant = () => {
    const id = uniqueId('case', variants.map(v => v.id))
    set({ variants: [...variants, { id, name: '' }] })
  }
  return (
    <div className="test-body">
      <div className="grid">
        <Field label="Name" wide><Text value={test.name} onChange={name => set({ name })} /></Field>
        <Field label="Type">
          <Select value={test.type} onChange={v => { if (v !== test.type && (variants.length === 0 || confirm('Changing the type clears this test’s fields, expectations and cases. Continue?'))) setType(v as TestType) }}
            options={(Object.keys(TYPE_LABEL) as TestType[]).map(t => [t, TYPE_LABEL[t]] as [string, string])} />
        </Field>
        <Field label="Kind" hint="Negative tests expect the call to be rejected."><Select value={kind} onChange={k => set({ kind: k === 'positive' ? undefined : k })} options={['positive', 'negative']} /></Field>
        <Field group label="Auth"><AuthSelect value={test.auth} onChange={auth => set({ auth })} profiles={profiles} inherited={inheritedAuth} /></Field>
      </div>

      <Section title={TYPE_LABEL[test.type]} open>
        <StepFields type={test.type} step={(test as any)[test.type]} protos={protos} set={patch => set({ [test.type]: { ...((test as any)[test.type] || {}), ...patch } } as any)} />
      </Section>

      <Section title="Expect" open count={(test.expect?.body?.length || 0)}>
        <ExpectEditor type={test.type} kind={kind} value={test.expect} onChange={expect => set({ expect })} />
      </Section>

      <Section title="Capture values for later steps" count={Object.keys(test.capture || {}).length}>
        <p className="hint">Save part of the result under a name; later steps use it as <code>{'{{name}}'}</code>. Paths work like checks; <code>$status</code> and <code>$header.Name</code> are also available.</p>
        <KV value={test.capture} onChange={capture => set({ capture })} keyLabel="variable name" valueLabel="path, e.g. id" addLabel="Add capture" />
      </Section>

      <Section title="Paired cases (same call, something changed)" open={variants.length > 0} count={variants.length}>
        <p className="hint">Each case re-runs this test with a few fields overridden, right after it. This is the quick way to add negative tests: write only what differs and what should happen.</p>
        {variants.map((v, i) => (
          <VariantEditor key={v.id} type={test.type} v={v} profiles={profiles} inherited={test.auth || inheritedAuth}
            onChange={nv => set({ variants: variants.map((o, j) => (j === i ? nv : o)) })}
            onRemove={() => { const n = variants.filter((_, j) => j !== i); set({ variants: n.length ? n : undefined }) }} />
        ))}
        <button type="button" onClick={addVariant}>+ Add case</button>
      </Section>

      <Section title="Properties and run options" count={Object.keys(test.properties || {}).length}>
        <Field group label="Properties" wide hint="Free-form labels such as priority, component, owner or a requirement id. They appear in results and JUnit reports.">
          <KV value={test.properties} onChange={properties => set({ properties })} keyLabel="property" addLabel="Add property" />
        </Field>
        <Field label="Description" wide><Text value={test.description} onChange={description => set({ description: description || undefined })} /></Field>
        <div className="grid">
          <Field label="Retry attempts" hint="Useful for checks that become true a moment later."><Num value={test.retry?.attempts} onChange={attempts => set({ retry: attempts && attempts > 1 ? { attempts, delayMs: test.retry?.delayMs } : undefined })} placeholder="1" /></Field>
          <Field label="Delay between attempts (ms)"><Num value={test.retry?.delayMs} onChange={delayMs => set({ retry: test.retry ? { ...test.retry, delayMs } : undefined })} placeholder="500" /></Field>
        </div>
        <div className="row">
          <Check label="Skip this test" value={test.skip} onChange={skip => set({ skip: skip || undefined })} />
          <Check label="Keep going if this test fails" value={test.continueOnFail} onChange={c => set({ continueOnFail: c || undefined })} />
        </div>
      </Section>
    </div>
  )
}

export function TypeBadge({ type }: { type: string }) { return <Badge kind={'t-' + type}>{type}</Badge> }
export function KindBadge({ kind }: { kind?: string }) { const k = kind || 'positive'; return <Badge kind={'k-' + k}>{k}</Badge> }
