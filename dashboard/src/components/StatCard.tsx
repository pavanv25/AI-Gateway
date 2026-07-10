interface StatCardProps {
  label: string
  value: string | number
  accent?: 'error'
}

export function StatCard({ label, value, accent }: StatCardProps) {
  return (
    <div className={`stat-card${accent ? ` stat-card-${accent}` : ''}`}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  )
}
