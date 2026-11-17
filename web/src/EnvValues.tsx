import type { Dict, Global } from './types'
import { KV } from './widgets'

/** How a key reads: a pair, or a value that applies across several pairs. */
export function keyLabel(key: string, g: Global): string {
  if (key.includes('/')) return key.replace('/', ' · ')
  if ((g.regions || []).length === 0) return key
  if ((g.regions || []).includes(key)) return `${key} (every environment in this region)`
  if ((g.environments || []).includes(key)) return `${key} (every region of this environment)`
  return key
}

/**
 * One card of variables per run target of the global list. Values stored
 * under other keys (a whole environment, a whole region, or a name that has
 * left the list) are shown after them so nothing is hidden.
 */
export function EnvValues({ global, values, onChange, defaultKey, noun }: {
  global: Global; values: Record<string, Dict> | undefined; onChange: (v: Record<string, Dict>) => void; defaultKey?: string; noun: string
}) {
  const vals = values || {}
  const keys = global.keys
  const names = [...(global.environments || []), ...(global.regions || [])]
  const extra = Object.keys(vals).filter(k => !keys.includes(k) && Object.keys(vals[k] || {}).length > 0)
  const setKey = (key: string, v: Dict | undefined) => {
    const next = { ...vals }
    if (v && Object.keys(v).length) next[key] = v; else delete next[key]
    onChange(next)
  }
  return (
    <>
      {keys.map(k => (
        <div className="card" key={k} data-env={k}>
          <div className="editor-bar"><h4>{keyLabel(k, global)}{defaultKey === k ? <span className="muted"> · default</span> : null}</h4></div>
          <KV value={vals[k]} onChange={v => setKey(k, v)} keyLabel="variable" addLabel="Add variable" />
        </div>
      ))}
      {extra.map(k => {
        const layered = names.includes(k)
        return (
          <div className="card" key={k} data-env={k}>
            <div className="editor-bar">
              <h4>{keyLabel(k, global)}{!layered && <span className="muted"> · not in the global list, so never used</span>}</h4>
              <button type="button" className="link danger" onClick={() => { if (confirm(`Remove the ${noun} kept under “${k}”?`)) setKey(k, undefined) }}>Remove</button>
            </div>
            {layered && <p className="hint">Applies to several targets at once; a value set for an exact target above wins.</p>}
            <KV value={vals[k]} onChange={v => setKey(k, v)} keyLabel="variable" addLabel="Add variable" />
          </div>
        )
      })}
    </>
  )
}
