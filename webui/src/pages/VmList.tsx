import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useVMs, useVMAction, useDeleteVM } from '../hooks/useApi'
import StatusBadge from '../components/StatusBadge'

type FilterTab = 'all' | 'vm' | 'container'

export default function VmList() {
  const { data: vms, isLoading, isError } = useVMs()
  const vmAction = useVMAction()
  const deleteVM = useDeleteVM()
  const [filter, setFilter] = useState<FilterTab>('all')
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null)

  const filteredVms = (vms ?? []).filter((vm) => {
    if (filter === 'all') return true
    if (filter === 'vm') return vm.type !== 'container'
    return vm.type === 'container'
  })

  const handleAction = (id: number, action: string) => {
    vmAction.mutate({ id, action })
  }

  const handleDelete = (id: number) => {
    if (confirmDelete === id) {
      deleteVM.mutate(id)
      setConfirmDelete(null)
    } else {
      setConfirmDelete(id)
    }
  }

  return (
    <div>
      <div className="mb-6 flex items-center justify-between">
        <h2 className="text-xl font-semibold text-slate-800">VMs / Containers</h2>
        <Link
          to="/vms/create"
          className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700"
        >
          + Create
        </Link>
      </div>

      {/* Filter tabs */}
      <div className="mb-4 flex gap-1 rounded-lg bg-slate-100 p-1 w-fit">
        {(['all', 'vm', 'container'] as FilterTab[]).map((tab) => (
          <button
            key={tab}
            onClick={() => setFilter(tab)}
            className={`rounded-md px-3 py-1 text-sm font-medium transition-colors ${
              filter === tab
                ? 'bg-white text-slate-900 shadow-sm'
                : 'text-slate-500 hover:text-slate-700'
            }`}
          >
            {tab === 'all' ? 'All' : tab === 'vm' ? 'VMs' : 'Containers'}
          </button>
        ))}
      </div>

      {isLoading && <p className="text-slate-500">Loading...</p>}
      {isError && (
        <p className="text-red-600">Failed to load VMs. Is the Controller running?</p>
      )}

      {!isLoading && !isError && (
        <div className="overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm">
          <table className="min-w-full divide-y divide-slate-200">
            <thead className="bg-slate-50">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">ID</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Name</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Type</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">State</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">vCPUs</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Memory</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Backend</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Node</th>
                <th className="px-4 py-3 text-left text-xs font-medium uppercase text-slate-500">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {filteredVms.length === 0 ? (
                <tr>
                  <td colSpan={9} className="px-4 py-8 text-center text-sm text-slate-400">
                    No VMs found. Create one to get started.
                  </td>
                </tr>
              ) : (
                filteredVms.map((vm) => (
                  <tr key={vm.handle} className="hover:bg-slate-50">
                    <td className="px-4 py-3 text-sm font-mono text-slate-600">{vm.handle}</td>
                    <td className="px-4 py-3 text-sm font-medium text-slate-900">{vm.name}</td>
                    <td className="px-4 py-3 text-sm">
                      <span
                        className={`inline-flex rounded px-1.5 py-0.5 text-xs font-medium ${
                          vm.type === 'container'
                            ? 'bg-purple-100 text-purple-700'
                            : 'bg-sky-100 text-sky-700'
                        }`}
                      >
                        {vm.type === 'container' ? 'CT' : 'VM'}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-sm">
                      <StatusBadge state={vm.state} />
                    </td>
                    <td className="px-4 py-3 text-sm text-slate-600">{vm.vcpus}</td>
                    <td className="px-4 py-3 text-sm text-slate-600">{vm.memory_mb} MB</td>
                    <td className="px-4 py-3 text-sm text-slate-500">{vm.backend}</td>
                    <td className="px-4 py-3 text-sm text-slate-500">{vm.node || '-'}</td>
                    <td className="px-4 py-3 text-sm">
                      <div className="flex gap-1">
                        <ActionBtn
                          label="Start"
                          onClick={() => handleAction(vm.handle, 'start')}
                          disabled={vm.state === 'running'}
                          color="green"
                        />
                        <ActionBtn
                          label="Stop"
                          onClick={() => handleAction(vm.handle, 'stop')}
                          disabled={vm.state === 'stopped' || vm.state === 'created'}
                          color="orange"
                        />
                        <button
                          onClick={() => handleDelete(vm.handle)}
                          className="rounded px-2 py-1 text-xs font-medium text-red-600 hover:bg-red-50"
                        >
                          {confirmDelete === vm.handle ? 'Confirm?' : 'Delete'}
                        </button>
                      </div>
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

function ActionBtn({
  label,
  onClick,
  disabled,
  color,
}: {
  label: string
  onClick: () => void
  disabled: boolean
  color: string
}) {
  const colorMap: Record<string, string> = {
    green: 'text-green-600 hover:bg-green-50',
    orange: 'text-orange-600 hover:bg-orange-50',
  }
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={`rounded px-2 py-1 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-40 ${colorMap[color] ?? ''}`}
    >
      {label}
    </button>
  )
}
