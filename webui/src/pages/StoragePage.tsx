import { usePools, useVolumes } from '../hooks/useApi'

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

export default function StoragePage() {
  const { data: pools, isLoading: poolsLoading } = usePools()
  const { data: volumes, isLoading: volsLoading } = useVolumes()

  return (
    <div>
      <h2 className="mb-6 text-xl font-semibold text-slate-800">Storage</h2>

      {/* Pools */}
      <h3 className="mb-3 text-sm font-semibold uppercase text-slate-500">Pools</h3>
      {poolsLoading ? (
        <p className="text-slate-500">Loading pools...</p>
      ) : (
        <div className="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {(pools ?? []).map((pool) => {
            const usedPct =
              pool.total_bytes > 0
                ? ((pool.used_bytes / pool.total_bytes) * 100).toFixed(1)
                : '0'
            return (
              <div
                key={pool.name}
                className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
              >
                <div className="flex items-center justify-between">
                  <p className="font-medium text-slate-900">{pool.name}</p>
                  <span className="rounded bg-slate-100 px-2 py-0.5 text-xs text-slate-600">
                    {pool.pool_type}
                  </span>
                </div>
                <div className="mt-3">
                  <div className="flex justify-between text-xs text-slate-500">
                    <span>{formatBytes(pool.used_bytes)} used</span>
                    <span>{formatBytes(pool.total_bytes)} total</span>
                  </div>
                  <div className="mt-1 h-2 rounded-full bg-slate-100">
                    <div
                      className="h-2 rounded-full bg-blue-500"
                      style={{ width: `${usedPct}%` }}
                    />
                  </div>
                </div>
                <p className="mt-2 text-xs text-slate-400">Health: {pool.health}</p>
              </div>
            )
          })}
        </div>
      )}

      {/* Volumes */}
      <h3 className="mb-3 text-sm font-semibold uppercase text-slate-500">Volumes</h3>
      {volsLoading ? (
        <p className="text-slate-500">Loading volumes...</p>
      ) : (
        <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-slate-200">
            <thead className="bg-slate-50">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">ID</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Name</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Pool</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Size</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Format</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {(volumes ?? []).length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-slate-400">
                    No volumes found.
                  </td>
                </tr>
              ) : (
                (volumes ?? []).map((vol) => (
                  <tr key={vol.id} className="hover:bg-slate-50">
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{vol.id}</td>
                    <td className="px-4 py-3 text-sm text-slate-900">{vol.name}</td>
                    <td className="px-4 py-3 text-sm text-slate-500">{vol.pool}</td>
                    <td className="px-4 py-3 text-sm text-slate-600">{formatBytes(vol.size_bytes)}</td>
                    <td className="px-4 py-3 text-sm text-slate-500">{vol.format}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
