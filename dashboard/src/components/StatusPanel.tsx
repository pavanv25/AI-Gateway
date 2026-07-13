import type { RateLimitStatus, CircuitStatus } from '../types'

interface Props {
  rateLimit: RateLimitStatus
  circuitBreakers: CircuitStatus[] | null
}

function stateClass(state: string): string {
  return 'breaker-' + state.replace('/', '-')
}

export function StatusPanel({ rateLimit, circuitBreakers }: Props) {
  const pct = rateLimit.Limit > 0 ? Math.min(100, (rateLimit.Used / rateLimit.Limit) * 100) : 0

  return (
    <div className="status-panel-card">
      <div className="status-panel-usage">
        <div className="status-panel-usage-label">
          <span>TPM Usage</span>
          <span className="muted">
            {rateLimit.Used.toLocaleString()} / {rateLimit.Limit.toLocaleString()}
          </span>
        </div>
        <div className="usage-bar">
          <div
            className={`usage-bar-fill${pct >= 90 ? ' usage-bar-fill-warn' : ''}`}
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>

      <div className="status-panel-breakers">
        {(circuitBreakers ?? []).map(cb => (
          <span key={cb.Provider} className={`breaker-badge ${stateClass(cb.State)}`}>
            {cb.Provider}: {cb.State}
          </span>
        ))}
      </div>
    </div>
  )
}
