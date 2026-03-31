import { useSnapshots, useDeleteSnapshot, useRestoreSnapshot } from '../hooks/useApi'

export default function SnapshotsPage() {
  const { data: snapshots, isLoading } = useSnapshots()
  const deleteSnap = useDeleteSnapshot()
  const restoreSnap = useRestoreSnapshot()

  if (isLoading) return <p className="p-6 text-slate-400">Loading snapshots...</p>

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold text-white">VM Snapshots</h1>
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm text-slate-300">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">VM</th>
              <th className="px-4 py-3">Created</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-700">
            {(snapshots || []).map((s: any) => (
              <tr key={s.id} className="hover:bg-slate-800/50">
                <td className="px-4 py-3 font-mono text-xs">{s.id}</td>
                <td className="px-4 py-3">{s.vm_name} (#{s.vm_id})</td>
                <td className="px-4 py-3 text-xs">{s.created_at}</td>
                <td className="px-4 py-3 space-x-2">
                  <button onClick={() => restoreSnap.mutate(s.id)}
                    className="rounded bg-green-600 px-2 py-1 text-xs text-white hover:bg-green-700">Restore</button>
                  <button onClick={() => deleteSnap.mutate(s.id)}
                    className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700">Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
