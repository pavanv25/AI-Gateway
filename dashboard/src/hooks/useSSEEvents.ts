import { useState, useEffect } from 'react'
import { connectSSEStream, SSEError } from '../api'
import type { MetricEvent, RateBucket } from '../types'

const MAX_EVENTS = 100
const RATE_WINDOW_MINUTES = 30

function toMinuteKey(ts: string): string {
  const d = new Date(ts)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function buildRateBuckets(events: MetricEvent[]): RateBucket[] {
  // Count requests and errors per minute from the event buffer.
  const counts = new Map<string, { requests: number; errors: number }>()
  for (const e of events) {
    const key = toMinuteKey(e.Timestamp)
    const prev = counts.get(key) ?? { requests: 0, errors: 0 }
    counts.set(key, {
      requests: prev.requests + 1,
      errors: prev.errors + (e.ErrorType ? 1 : 0),
    })
  }

  // Backfill every minute in the rolling window so the x-axis is uniform
  // even when no events arrive (those minutes render as zero).
  const now = new Date()
  const result: RateBucket[] = []
  for (let i = RATE_WINDOW_MINUTES - 1; i >= 0; i--) {
    const d = new Date(now.getTime() - i * 60_000)
    const key = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
    const bucket = counts.get(key)
    result.push({ minute: key, requests: bucket?.requests ?? 0, errors: bucket?.errors ?? 0 })
  }
  return result
}

export function useSSEEvents(apiKey: string) {
  const [events, setEvents] = useState<MetricEvent[]>([])
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    if (!apiKey) return

    let mounted = true
    let controller = new AbortController()

    function connect() {
      if (!mounted) return
      controller = new AbortController()
      setConnected(true)

      connectSSEStream(apiKey, controller.signal, (event) => {
        setEvents(prev => [event, ...prev].slice(0, MAX_EVENTS))
      })
        .then(() => {
          setConnected(false)
          // Gateway closed the stream (e.g. restart) — reconnect after delay.
          if (mounted) setTimeout(connect, 2_000)
        })
        .catch((err: unknown) => {
          setConnected(false)
          if (!mounted) return
          if (err instanceof DOMException && err.name === 'AbortError') return
          // 4xx (wrong key, missing key) — stop retrying, surface the error in UI via connected=false.
          if (err instanceof SSEError && err.status >= 400 && err.status < 500) return
          if (mounted) setTimeout(connect, 2_000)
        })
    }

    connect()
    return () => {
      mounted = false
      controller.abort()
    }
  }, [apiKey])

  const rateBuckets = buildRateBuckets(events)

  return { events, rateBuckets, connected }
}
