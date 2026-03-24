import { useClusterStatus, useClusterNodes } from '../hooks/useApi'

export default function ClusterPage() {
  const { data: status, isLoading: statusLoading } = useClusterStatus()
  const { data: nodes, isLoading: nodesLoading } = useClusterNodes()

  return (
    <div>
      <h2 className="mb-6 text-xl font-semibold text-slate-800">Cluster</h2>

      {/* Status cards */}
      {statusLoading ? (
        <p className="text-slate-500">Loading cluster status...</p>
      ) : status ? (
        <div className="mb-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <p className="text-sm text-slate-500">Quorum</p>
            <p className={`mt-1 text-2xl font-semibold ${status.quorum ? 'text-green-600' : 'text-red-600'}`}>
              {status.quorum ? 'OK' : 'LOST'}
            </p>
          </div>
          <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <p className="text-sm text-slate-500">Nodes</p>
            <p className="mt-1 text-2xl font-semibold text-slate-900">
              {status.online_count} / {status.node_count}
            </p>
          </div>
          <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <p className="text-sm text-slate-500">Leader</p>
            <p className="mt-1 text-lg font-semibold text-slate-900">{status.leader || '-'}</p>
          </div>
          <div className="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <p className="text-sm text-slate-500">Health</p>
            <p
              className={`mt-1 text-2xl font-semibold ${
                status.health === 'healthy' ? 'text-green-600' : 'text-yellow-600'
              }`}
            >
              {status.health}
            </p>
          </div>
        </div>
      ) : null}

      {/* Nodes table */}
      <h3 className="mb-3 text-sm font-semibold uppercase text-slate-500">Nodes</h3>
      {nodesLoading ? (
        <p className="text-slate-500">Loading nodes...</p>
      ) : (
        <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-slate-200">
            <thead className="bg-slate-50">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Name</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Address</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Status</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Role</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">vCPUs</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Memory</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {(nodes ?? []).length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-6 text-center text-sm text-slate-400">
                    No nodes registered.
                  </td>
                </tr>
              ) : (
                (nodes ?? []).map((node) => (
                  <tr key={node.id} className="hover:bg-slate-50">
                    <td className="px-4 py-3 text-sm font-medium text-slate-900">{node.name}</td>
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{node.address}</td>
                    <td className="px-4 py-3 text-sm">
                      <span
                        className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${
                          node.status === 'online'
                            ? 'bg-green-100 text-green-700'
                            : 'bg-red-100 text-red-700'
                        }`}
                      >
                        {node.status}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-sm text-slate-500">{node.role}</td>
                    <td className="px-4 py-3 text-sm text-slate-600">
                      {node.vcpus_used} / {node.vcpus_total}
                    </td>
                    <td className="px-4 py-3 text-sm text-slate-600">
                      {node.memory_used_mb} / {node.memory_total_mb} MB
                    </td>
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
