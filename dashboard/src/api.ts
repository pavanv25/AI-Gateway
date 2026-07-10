import type { MetricEvent, Snapshot } from './types'

class SSEError extends Error {
  readonly status: number
  constructor(status: number) {
    super(`SSE ${status}`)
    this.name = 'SSEError'
    this.status = status
  }
}

export async function fetchSnapshot(apiKey: string, queryWindow: string): Promise<Snapshot> {
  const res = await fetch(`/v1/metrics?window=${queryWindow}`, {
    headers: { 'X-API-Key': apiKey },
  })
  if (!res.ok) throw new Error(`metrics ${res.status}`)
  return res.json()
}

export async function connectSSEStream(
  apiKey: string,
  signal: AbortSignal,
  onEvent: (e: MetricEvent) => void
): Promise<void> {
  const res = await fetch('/v1/metrics/stream', {
    headers: { 'X-API-Key': apiKey },
    signal,
  })
  if (!res.ok) throw new SSEError(res.status)

  // Gin's SSE encoder writes "data:{json}\n" (no space after colon).
  const reader = res.body!.pipeThrough(new TextDecoderStream()).getReader()
  let buffer = ''
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += value
      const lines = buffer.split('\n')
      buffer = lines.pop() ?? ''
      for (const line of lines) {
        if (line.startsWith('data:')) {
          try {
            onEvent(JSON.parse(line.slice(5)))
          } catch {
            // ignore malformed JSON
          }
        }
      }
    }
  } finally {
    reader.releaseLock()
  }
}

export { SSEError }
