import { useState } from 'react'
import type { RunResult, TestResult } from './types'
import { StatusBadge } from './widgets'
import { KindBadge, TypeBadge } from './TestEditor'

function pretty(s: string | undefined): string {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

function TestRow({ t }: { t: TestResult }) {
  const failed = t.status === 'failed' || t.status === 'error'
  const [open, setOpen] = useState(failed)
  return (
    <div className={'result-row' + (t.variantOf ? ' variant-row' : '')}>
      <button type="button" className="result-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="chev">{open ? '▾' : '▸'}</span>
        <StatusBadge status={t.status} />
        <TypeBadge type={t.type} /> <KindBadge kind={t.kind} />
        <span className="result-name">{t.name || t.id}</span>
        <span className="muted">{t.durationMs} ms{t.attempts && t.attempts > 1 ? ` · ${t.attempts} attempts` : ''}</span>
      </button>
      {open && (
        <div className="result-body">
          {t.error && <p className={t.status === 'skipped' ? 'muted' : 'error-text'}>{t.error}</p>}
          {t.assertions && t.assertions.length > 0 && (
            <table className="table">
              <thead><tr><th></th><th>Check</th><th>Expected</th><th>Actual</th></tr></thead>
              <tbody>
                {t.assertions.map((a, i) => (
                  <tr key={i} className={a.passed ? '' : 'row-bad'}>
                    <td>{a.passed ? '✓' : '✗'}</td><td>{a.check}</td><td className="mono">{a.expected}</td><td className="mono">{a.actual}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {t.captured && Object.keys(t.captured).length > 0 && (
            <p className="muted">Captured: {Object.entries(t.captured).map(([k, v]) => <code key={k}>{k} = {v} </code>)}</p>
          )}
          {t.properties && Object.keys(t.properties).length > 0 && (
            <p className="muted">Properties: {Object.entries(t.properties).map(([k, v]) => <code key={k}>{k}: {v} </code>)}</p>
          )}
          {t.request !== undefined && (
            <details><summary>Request{t.auth ? ` (auth: ${t.auth})` : ''}</summary><pre>{JSON.stringify(t.request, null, 2)}</pre></details>
          )}
          {t.response && (
            <details open={failed}>
              <summary>Response: {t.response.statusText || t.response.status} in {t.response.durationMs} ms</summary>
              <pre>{pretty(t.response.body) || '(empty body)'}</pre>
            </details>
          )}
        </div>
      )}
    </div>
  )
}

export function RunView({ run }: { run: RunResult }) {
  return (
    <div className="run">
      <div className="run-summary">
        <StatusBadge status={run.status} />
        <strong>{run.summary.passed} passed</strong>
        <span className={run.summary.failed ? 'error-text' : 'muted'}>{run.summary.failed} failed</span>
        <span className="muted">{run.summary.skipped} skipped</span>
        <span className="muted">{run.summary.total} tests · {(run.durationMs / 1000).toFixed(2)} s{run.environment ? ` · ${run.environment}` : ''} · {new Date(run.startedAt).toLocaleString()}</span>
      </div>
      {run.scenarios.map(sc => (
        <div key={sc.id} className="run-scenario">
          <h4><StatusBadge status={sc.status} /> {sc.name} <span className="muted">{sc.durationMs} ms</span></h4>
          {(sc.tests || []).map(t => <TestRow key={t.id} t={t} />)}
        </div>
      ))}
      {run.scenarios.length === 0 && <p className="muted">No scenarios matched.</p>}
    </div>
  )
}
