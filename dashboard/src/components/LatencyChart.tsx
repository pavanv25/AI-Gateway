import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import type { Aggregate } from '../types'

interface Props {
  totals: Aggregate | null
}

const BARS = [
  { key: 'RequestLatencyP50', label: 'Req P50' },
  { key: 'RequestLatencyP95', label: 'Req P95' },
  { key: 'ProviderLatencyP50', label: 'Prov P50' },
  { key: 'ProviderLatencyP95', label: 'Prov P95' },
] as const

export function LatencyChart({ totals }: Props) {
  const data = BARS.map(b => ({
    name: b.label,
    ms: totals ? Number((totals[b.key] as number).toFixed(1)) : 0,
  }))

  return (
    <ResponsiveContainer width="100%" height={200}>
      <BarChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <XAxis dataKey="name" tick={{ fontSize: 11 }} tickLine={false} />
        <YAxis tick={{ fontSize: 10 }} width={36} tickLine={false} unit=" ms" />
        <Tooltip formatter={(v) => [`${v} ms`, 'Latency']} />
        <Bar dataKey="ms" radius={[4, 4, 0, 0]}>
          {data.map((_, i) => (
            <Cell key={i} fill={i % 2 === 0 ? 'var(--accent)' : 'var(--accent-dim)'} />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  )
}
