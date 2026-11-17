import { useEffect, useState } from 'react'
import { api, when } from './api'
import type { RunQuery, RunResult } from './types'
import { RunView } from './RunView'
import { Field, Notice, Select, StatusBadge, Text, useAction } from './widgets'

/**
 * Run history kept on the backend. With a scenario it shows that scenario's
 * past runs; without, every run of the application.
 */
export function History({ app, scenario, envs, refresh }: { app: string; scenario?: string; envs: string[]; refresh?: string }) {
  const [q, setQ] = useState<RunQuery>({})
  const [runs, setRuns] = useState<RunResult[] | null>(null)
  const [detail, setDetail] = useState<RunResult | null>(null)
  const act = useAction()

  useEffect(() => {
    act.run(async () => setRuns(await api.runs(app, { ...q, scenario, limit: 200 })))
  }, [app, scenario, refresh, q.status, q.env, q.from, q.to]) // eslint-disable-line
  useEffect(() => { setDetail(null) }, [app, scenario])

  const open = (id: string) => act.run(async () => {
    const full = await api.getRun(app, id)
    setDetail(scenario ? { ...full, scenarios: full.scenarios.filter(s => s.id === scenario) } : full)
  })
  const passed = (runs || []).filter(r => r.status === 'passed').length

  return (
    <div>
      <div className="grid filters">
        <Field label="Result"><Select value={q.status || ''} onChange={status => setQ({ ...q, status })} options={[['', 'any'], 'passed', 'failed']} /></Field>
        <Field label="Environment"><Select value={q.env || ''} onChange={env => setQ({ ...q, env })} options={[['', 'any'], ...envs]} /></Field>
        <Field label="From"><Text type="date" value={q.from} onChange={from => setQ({ ...q, from })} /></Field>
        <Field label="To"><Text type="date" value={q.to} onChange={to => setQ({ ...q, to })} /></Field>
      </div>
      {act.error && <Notice kind="error" onClose={() => act.setError('')}>{act.error}</Notice>}
      {runs && runs.length === 0 && <p className="muted">{q.status || q.env || q.from || q.to ? 'No runs match these filters.' : scenario ? 'This scenario has not been run on this backend yet.' : 'No runs on this backend yet.'}</p>}
      {runs && runs.length > 0 && (
        <>
          <p className="muted">{runs.length} run{runs.length === 1 ? '' : 's'}{runs.length === 200 ? ' (latest 200)' : ''}, {passed} passed, {runs.length - passed} failed.</p>
          <table className="table history">
            <thead><tr><th>When</th><th>Result</th><th>Tests</th><th>Environment</th><th>Started by</th><th>Duration</th></tr></thead>
            <tbody>
              {runs.map(r => (
                <tr key={r.id} className={detail?.id === r.id ? 'row-on' : ''}>
                  <td><button type="button" className="link" onClick={() => open(r.id)}>{when(r.startedAt)}</button></td>
                  <td><StatusBadge status={r.status} /></td>
                  <td>{r.summary.passed}/{r.summary.total} passed{r.summary.skipped ? `, ${r.summary.skipped} skipped` : ''}</td>
                  <td>{r.environment || ''}</td>
                  <td>{r.triggeredBy || <span className="muted">unknown</span>}{r.source === 'cli' ? ' (pipeline)' : ''}</td>
                  <td>{(r.durationMs / 1000).toFixed(2)} s</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {detail && (
        <div className="card">
          <div className="editor-bar"><h3>Run of {when(detail.startedAt)}</h3><button type="button" className="link" onClick={() => setDetail(null)}>Hide</button></div>
          <RunView run={detail} />
        </div>
      )}
    </div>
  )
}
