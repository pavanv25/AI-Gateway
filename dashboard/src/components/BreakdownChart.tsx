import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import type { BreakdownEntry } from '../types'

interface Props {
  breakdowns: BreakdownEntry[]
}

export function BreakdownChart({ breakdowns }: Props) {
  if (breakdowns.length === 0) {
    return <div className="chart-empty">No data yet — send a request to see breakdown</div>
  }

  const data = breakdowns
    .map(b => ({ name: `${b.Provider}/${b.Model || 'default'}`, Requests: b.RequestCount }))
    .sort((a, b) => b.Requests - a.Requests)

  return (
    <ResponsiveContainer width="100%" height={Math.max(160, data.length * 44)}>
      <BarChart
        data={data}
        layout="vertical"
        margin={{ top: 4, right: 16, left: 0, bottom: 0 }}
      >
        <XAxis type="number" allowDecimals={false} tick={{ fontSize: 10 }} tickLine={false} />
        <YAxis
          type="category"
          dataKey="name"
          tick={{ fontSize: 10 }}
          width={148}
          tickLine={false}
        />
        <Tooltip />
        <Bar dataKey="Requests" fill="var(--accent)" radius={[0, 4, 4, 0]} />
      </BarChart>
    </ResponsiveContainer>
  )
}
