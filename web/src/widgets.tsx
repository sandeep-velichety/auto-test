import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { Dict } from './types'

export function Field({ label, hint, children, wide, group }: { label: string; hint?: string; children: ReactNode; wide?: boolean; group?: boolean }) {
  const cls = 'field' + (wide ? ' wide' : '')
  // A <label> may wrap only one control; editors with several use a group.
  if (group) {
    return (
      <div className={cls} role="group" aria-label={label}>
        <span className="field-label">{label}</span>
        {children}
        {hint && <span className="hint">{hint}</span>}
      </div>
    )
  }
  return (
    <label className={cls}>
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="hint">{hint}</span>}
    </label>
  )
}

export function Text({ value, onChange, placeholder, type, mono, disabled }: {
  value: string | undefined; onChange: (v: string) => void; placeholder?: string; type?: string; mono?: boolean; disabled?: boolean
}) {
  return <input className={mono ? 'mono' : ''} type={type || 'text'} value={value ?? ''} placeholder={placeholder} disabled={disabled}
    onChange={e => onChange(e.target.value)} spellCheck={false} autoComplete="off" />
}

export function Num({ value, onChange, placeholder }: { value: number | undefined; onChange: (v: number | undefined) => void; placeholder?: string }) {
  return <input type="number" value={value ?? ''} placeholder={placeholder}
    onChange={e => onChange(e.target.value === '' ? undefined : Number(e.target.value))} />
}

export function Select({ value, onChange, options }: { value: string | undefined; onChange: (v: string) => void; options: (string | [string, string])[] }) {
  return (
    <select value={value ?? ''} onChange={e => onChange(e.target.value)}>
      {options.map(o => {
        const [v, l] = typeof o === 'string' ? [o, o] : o
        return <option key={v} value={v}>{l}</option>
      })}
    </select>
  )
}

export function Check({ label, value, onChange }: { label: string; value: boolean | undefined; onChange: (v: boolean) => void }) {
  return (
    <label className="check">
      <input type="checkbox" checked={!!value} onChange={e => onChange(e.target.checked)} /> {label}
    </label>
  )
}

/** Key/value editor. Rows keep their order and may be half-filled while typing. */
export function KV({ value, onChange, keyLabel = 'name', valueLabel = 'value', addLabel = 'Add' }: {
  value: Dict | undefined; onChange: (v: Dict | undefined) => void; keyLabel?: string; valueLabel?: string; addLabel?: string
}) {
  const [rows, setRows] = useState<[string, string][]>(() => Object.entries(value || {}))
  const update = (next: [string, string][]) => {
    setRows(next)
    const out: Dict = {}
    for (const [k, v] of next) if (k.trim()) out[k.trim()] = v
    onChange(Object.keys(out).length ? out : undefined)
  }
  return (
    <div className="kv">
      {rows.map(([k, v], i) => (
        <div className="kv-row" key={i}>
          <input className="mono" value={k} placeholder={keyLabel} spellCheck={false} onChange={e => update(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))} />
          <input className="mono" value={v} placeholder={valueLabel} spellCheck={false} onChange={e => update(rows.map((r, j) => (j === i ? [r[0], e.target.value] : r)))} />
          <button type="button" className="icon" title="Remove" onClick={() => update(rows.filter((_, j) => j !== i))}>×</button>
        </div>
      ))}
      <button type="button" className="link" onClick={() => setRows([...rows, ['', '']])}>+ {addLabel}</button>
    </div>
  )
}

/**
 * Textarea for a JSON value. With allowText, anything that is not JSON is
 * kept as a plain string (useful for raw bodies with {{variables}} in them).
 */
export function JsonField({ value, onChange, allowText, rows = 5, placeholder }: {
  value: any; onChange: (v: any) => void; allowText?: boolean; rows?: number; placeholder?: string
}) {
  const [text, setText] = useState(() => (value === undefined || value === null ? '' : typeof value === 'string' ? value : JSON.stringify(value, null, 2)))
  const [bad, setBad] = useState(false)
  const change = (t: string) => {
    setText(t)
    if (t.trim() === '') { setBad(false); onChange(undefined); return }
    try { onChange(JSON.parse(t)); setBad(false) } catch {
      if (allowText) { onChange(t); setBad(false) } else setBad(true)
    }
  }
  return (
    <div>
      <textarea className={'mono' + (bad ? ' invalid' : '')} rows={rows} value={text} placeholder={placeholder} spellCheck={false} onChange={e => change(e.target.value)} />
      {bad && <span className="hint error">Not valid JSON yet. The last valid value is kept.</span>}
    </div>
  )
}

export function Badge({ kind, children }: { kind: string; children: ReactNode }) {
  return <span className={'badge ' + kind}>{children}</span>
}

export function StatusBadge({ status }: { status: string }) {
  return <Badge kind={'st-' + status}>{status}</Badge>
}

export function Section({ title, children, open = false, count }: { title: string; children: ReactNode; open?: boolean; count?: number }) {
  const [isOpen, setOpen] = useState(open)
  return (
    <div className="section">
      <button type="button" className="section-head" onClick={() => setOpen(!isOpen)}>
        <span className="chev">{isOpen ? '▾' : '▸'}</span> {title}
        {count ? <span className="count">{count}</span> : null}
      </button>
      {isOpen && <div className="section-body">{children}</div>}
    </div>
  )
}

export function Notice({ kind, children, onClose }: { kind: 'error' | 'ok' | 'info'; children: ReactNode; onClose?: () => void }) {
  return (
    <div className={'notice ' + kind} role={kind === 'error' ? 'alert' : 'status'}>
      <div>{children}</div>
      {onClose && <button type="button" className="icon" onClick={onClose} title="Dismiss">×</button>}
    </div>
  )
}

export function FileButton({ label, accept, onFile }: { label: string; accept?: string; onFile: (f: File) => void }) {
  const ref = useRef<HTMLInputElement>(null)
  return (
    <>
      <input ref={ref} type="file" accept={accept} hidden onChange={e => { const f = e.target.files?.[0]; if (f) onFile(f); e.target.value = '' }} />
      <button type="button" onClick={() => ref.current?.click()}>{label}</button>
    </>
  )
}

/** Runs an async action and exposes busy/error state. */
export function useAction() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  const run = async (fn: () => Promise<void>) => {
    setBusy(true); setError('')
    try { await fn() } catch (e: any) { if (alive.current) setError(e?.message || String(e)) } finally { if (alive.current) setBusy(false) }
  }
  return { busy, error, setError, run }
}
