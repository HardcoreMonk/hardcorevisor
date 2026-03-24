import { useSystemStats } from '../hooks/useApi'

function StatCard({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
      <p className="text-sm font-medium text-slate-500">{label}</p>
      <p className="mt-1 text-3xl font-semibold text-slate-900">{value}</p>
      {sub && <p className="mt-1 text-xs text-slate-400">{sub}</p>}
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export default function Dashboard() {
  const { data: stats, isLoading, isError } = useSystemStats()

  if (isLoading) {
    return <div className="text-slate-500">Loading dashboard...</div>
  }

  if (isError || !stats) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700">
        Failed to connect to Controller. Is it running on localhost:8080?
      </div>
    )
  }

  const storageUsedPct =
    stats.total_storage_bytes > 0
      ? ((stats.used_storage_bytes / stats.total_storage_bytes) * 100).toFixed(1)
      : '0'

  return (
    <div>
      <h2 className="mb-6 text-xl font-semibold text-slate-800">Dashboard</h2>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label="Total VMs"
          value={stats.total_vms}
          sub={`${stats.running_vms} running / ${stats.stopped_vms} stopped`}
        />
        <StatCard
          label="Running VMs"
          value={stats.running_vms}
        />
        <StatCard
          label="Nodes Online"
          value={`${stats.online_nodes} / ${stats.total_nodes}`}
        />
        <StatCard
          label="Storage Pools"
          value={stats.storage_pools}
          sub={`${formatBytes(stats.used_storage_bytes)} / ${formatBytes(stats.total_storage_bytes)} (${storageUsedPct}%)`}
        />
      </div>

      <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <p className="text-sm font-medium text-slate-500">System Uptime</p>
          <p className="mt-1 text-2xl font-semibold text-slate-900">
            {formatUptime(stats.uptime_seconds)}
          </p>
        </div>
        <div className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm">
          <p className="text-sm font-medium text-slate-500">Quick Info</p>
          <div className="mt-2 space-y-1 text-sm text-slate-600">
            <p>REST API: :8080 | gRPC: :9090</p>
            <p>Backends: rustvmm, qemu, lxc</p>
          </div>
        </div>
      </div>
    </div>
  )
}
