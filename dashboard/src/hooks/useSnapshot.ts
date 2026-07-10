import { useState, useEffect } from 'react'
import { fetchSnapshot } from '../api'
import type { Snapshot } from '../types'

export function useSnapshot(apiKey: string, queryWindow: string) {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!apiKey) return
    let cancelled = false

    async function poll() {
      try {
        const snap = await fetchSnapshot(apiKey, queryWindow)
        if (!cancelled) {
          setSnapshot(snap)
          setError(null)
        }
      } catch (e) {
        if (!cancelled) setError(String(e))
      }
    }

    poll()
    const id = setInterval(poll, 15_000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [apiKey, queryWindow])

  return { snapshot, error }
}
