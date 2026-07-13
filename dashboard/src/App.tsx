import { useState } from 'react'
import { useSnapshot } from './hooks/useSnapshot'
import { useSSEEvents } from './hooks/useSSEEvents'
import { StatCard } from './components/StatCard'
import { RequestRateChart } from './components/RequestRateChart'
import { LatencyChart } from './components/LatencyChart'
import { BreakdownChart } from './components/BreakdownChart'
import { KeyBreakdownChart } from './components/KeyBreakdownChart'
import { StatusPanel } from './components/StatusPanel'
import { EventLog } from './components/EventLog'

function useLocalStorage(key: string, initial: string): [string, (v: string) => void] {
  const [val, setVal] = useState(() => localStorage.getItem(key) ?? initial)
  const set = (v: string) => {
    localStorage.setItem(key, v)
    setVal(v)
  }
  return [val, set]
}

export default function App() {
  const [apiKey, setApiKey] = useLocalStorage('gateway-api-key', '')
  const [queryWindow, setQueryWindow] = useState('5m')

  const { snapshot, error: snapshotError } = useSnapshot(apiKey, queryWindow)
  const { events, rateBuckets, connected } = useSSEEvents(apiKey)

  const totals = snapshot?.Totals ?? null
  const breakdowns = snapshot?.Breakdowns ?? []
  const keyBreakdowns = snapshot?.KeyBreakdowns ?? []
  const rateLimit = snapshot?.RateLimit ?? { Used: 0, Limit: 0 }
  const circuitBreakers = snapshot?.CircuitBreakers ?? null

  const totalCacheOps = (totals?.CacheHits ?? 0) + (totals?.CacheMisses ?? 0)
  const cacheHitRate =
    totalCacheOps > 0
      ? `${((totals!.CacheHits / totalCacheOps) * 100).toFixed(1)}%`
      : '—'

  return (
    <div className="container">
      <header>
        <div className="header-left">
          <h1>AI Gateway</h1>
          <span className={`status-badge ${connected ? 'status-live' : 'status-offline'}`}>
            {connected ? '● Live' : '○ Offline'}
          </span>
        </div>
        <div className="header-controls">
          <select
            value={queryWindow}
            onChange={e => setQueryWindow(e.target.value)}
            aria-label="Time window"
          >
            <option value="1m">1 min</option>
            <option value="5m">5 min</option>
            <option value="15m">15 min</option>
            <option value="30m">30 min</option>
            <option value="1h">1 hour</option>
          </select>
          <input
            type="password"
            placeholder="X-API-Key"
            value={apiKey}
            onChange={e => setApiKey(e.target.value)}
            autoComplete="off"
            aria-label="API key"
          />
        </div>
      </header>

      {!apiKey && (
        <div className="empty-state">Enter your API key above to start monitoring.</div>
      )}

      {snapshotError && <div className="error-banner">{snapshotError}</div>}

      {apiKey && (
        <>
          <StatusPanel rateLimit={rateLimit} circuitBreakers={circuitBreakers} />

          <div className="stat-row">
            <StatCard label="Requests" value={totals?.RequestCount ?? 0} />
            <StatCard
              label="Errors"
              value={totals?.ErrorCount ?? 0}
              accent={totals?.ErrorCount ? 'error' : undefined}
            />
            <StatCard label="Cache Hit Rate" value={cacheHitRate} />
            <StatCard
              label="Total Tokens"
              value={(totals?.TotalTokens ?? 0).toLocaleString()}
            />
            <StatCard
              label={`Cost (${queryWindow})`}
              value={`$${(totals?.CostUSD ?? 0).toFixed(4)}`}
            />
          </div>

          <div className="charts-grid">
            <div className="chart-card">
              <h2>Request &amp; Error Rate (last 30 min)</h2>
              <RequestRateChart data={rateBuckets} />
            </div>
            <div className="chart-card">
              <h2>Latency</h2>
              <LatencyChart totals={totals} />
            </div>
            <div className="chart-card">
              <h2>Provider / Model Breakdown</h2>
              <BreakdownChart breakdowns={breakdowns} />
            </div>
            <div className="chart-card">
              <h2>Usage by API Key</h2>
              <KeyBreakdownChart breakdowns={keyBreakdowns} />
            </div>
          </div>

          <div className="event-log-card">
            <h2>
              Live Events{' '}
              <span className="muted" style={{ fontWeight: 400, textTransform: 'none' }}>
                ({events.length} buffered)
              </span>
            </h2>
            <EventLog events={events} />
          </div>
        </>
      )}
    </div>
  )
}
