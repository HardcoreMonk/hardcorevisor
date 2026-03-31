import { useState } from 'react'
import { useBackups, useCreateBackup, useDeleteBackup } from '../hooks/useApi'

export default function BackupsPage() {
  const { data: backups, isLoading } = useBackups()
  const createBackup = useCreateBackup()
  const deleteBackup = useDeleteBackup()
  const [vmId, setVmId] = useState('')
  const [vmName, setVmName] = useState('')
  const [pool, setPool] = useState('local-zfs')

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault()
    createBackup.mutate({ vm_id: Number(vmId), vm_name: vmName, pool })
    setVmId(''); setVmName('')
  }

  if (isLoading) return <p className="p-6 text-slate-400">Loading backups...</p>

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold text-white">Backups</h1>
      <form onSubmit={handleCreate} className="flex gap-3">
        <input value={vmId} onChange={e => setVmId(e.target.value)} placeholder="VM ID" type="number"
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white w-24" required />
        <input value={vmName} onChange={e => setVmName(e.target.value)} placeholder="VM Name"
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white" required />
        <input value={pool} onChange={e => setPool(e.target.value)} placeholder="Pool"
          className="rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-white w-32" />
        <button type="submit" className="rounded-lg bg-blue-600 px-4 py-2 text-white hover:bg-blue-700">
          Create Backup
        </button>
      </form>
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm text-slate-300">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">VM</th>
              <th className="px-4 py-3">Pool</th>
              <th className="px-4 py-3">Created</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-700">
            {(backups || []).map((b: any) => (
              <tr key={b.id} className="hover:bg-slate-800/50">
                <td className="px-4 py-3 font-mono text-xs">{b.id}</td>
                <td className="px-4 py-3">{b.vm_name} (#{b.vm_id})</td>
                <td className="px-4 py-3">{b.pool}</td>
                <td className="px-4 py-3 text-xs">{b.created_at}</td>
                <td className="px-4 py-3">
                  <button onClick={() => deleteBackup.mutate(b.id)}
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
