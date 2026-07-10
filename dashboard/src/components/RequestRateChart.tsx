import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts'
import type { RateBucket } from '../types'

interface Props {
  data: RateBucket[]
}

export function RequestRateChart({ data }: Props) {
  return (
    <ResponsiveContainer width="100%" height={200}>
      <LineChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <XAxis
          dataKey="minute"
          tick={{ fontSize: 10 }}
          interval={Math.floor(data.length / 6)}
          tickLine={false}
        />
        <YAxis allowDecimals={false} tick={{ fontSize: 10 }} width={28} tickLine={false} />
        <Tooltip />
        <Legend wrapperStyle={{ fontSize: 12 }} />
        <Line
          type="monotone"
          dataKey="requests"
          stroke="var(--accent)"
          dot={false}
          strokeWidth={2}
        />
        <Line
          type="monotone"
          dataKey="errors"
          stroke="var(--error)"
          dot={false}
          strokeWidth={2}
        />
      </LineChart>
    </ResponsiveContainer>
  )
}
