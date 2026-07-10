import type { MetricEvent } from '../types'

interface Props {
  events: MetricEvent[]
}

export function EventLog({ events }: Props) {
  if (events.length === 0) {
    return <div className="chart-empty">Waiting for events…</div>
  }

  return (
    <div className="table-scroll">
      <table className="event-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Provider</th>
            <th>Model</th>
            <th>Tokens</th>
            <th>Cost</th>
            <th>Latency</th>
            <th>Cache</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          {events.map((e, i) => (
            <tr key={i} className={e.ErrorType ? 'row-error' : ''}>
              <td className="td-mono">{new Date(e.Timestamp).toLocaleTimeString()}</td>
              <td>{e.Provider}</td>
              <td>{e.Model || <span className="muted">—</span>}</td>
              <td className="td-mono">{e.TotalTokens}</td>
              <td className="td-mono">
                {e.CostUSD > 0 ? `$${e.CostUSD.toFixed(5)}` : <span className="muted">—</span>}
              </td>
              <td className="td-mono">{e.RequestLatencyMs.toFixed(0)} ms</td>
              <td>{e.CacheHit ? <span className="badge-yes">hit</span> : <span className="muted">—</span>}</td>
              <td>{e.ErrorType || <span className="muted">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
