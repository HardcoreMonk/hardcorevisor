import { useDevices, useAttachDevice, useDetachDevice } from '../hooks/useApi'
import StatusBadge from '../components/StatusBadge'

export default function DevicesPage() {
  const { data: devices, isLoading } = useDevices()
  const attach = useAttachDevice()
  const detach = useDetachDevice()

  if (isLoading) return <p className="p-6 text-slate-400">Loading devices...</p>

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold text-white">Device Passthrough</h1>
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm text-slate-300">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">Type</th>
              <th className="px-4 py-3">Name</th>
              <th className="px-4 py-3">IOMMU Group</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">VM</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-700">
            {(devices || []).map((d: any) => (
              <tr key={d.id} className="hover:bg-slate-800/50">
                <td className="px-4 py-3 font-mono text-xs">{d.id}</td>
                <td className="px-4 py-3"><StatusBadge state={d.type} /></td>
                <td className="px-4 py-3">{d.name}</td>
                <td className="px-4 py-3">{d.iommu_group}</td>
                <td className="px-4 py-3"><StatusBadge state={d.status} /></td>
                <td className="px-4 py-3">{d.vm_handle || '-'}</td>
                <td className="px-4 py-3 space-x-2">
                  {d.status === 'available' ? (
                    <button
                      onClick={() => attach.mutate({ id: d.id, vmHandle: 1 })}
                      className="rounded bg-blue-600 px-2 py-1 text-xs text-white hover:bg-blue-700"
                    >Attach</button>
                  ) : (
                    <button
                      onClick={() => detach.mutate(d.id)}
                      className="rounded bg-orange-600 px-2 py-1 text-xs text-white hover:bg-orange-700"
                    >Detach</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
