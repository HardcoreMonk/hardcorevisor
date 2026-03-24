import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useCreateVM } from '../hooks/useApi'

export default function VmCreate() {
  const navigate = useNavigate()
  const createVM = useCreateVM()

  const [name, setName] = useState('')
  const [vmType, setVmType] = useState<'vm' | 'container'>('vm')
  const [vcpus, setVcpus] = useState(2)
  const [memoryMb, setMemoryMb] = useState(4096)
  const [backend, setBackend] = useState('auto')

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    createVM.mutate(
      {
        name,
        vcpus,
        memory_mb: memoryMb,
        backend: backend === 'auto' ? undefined : backend,
        type: vmType === 'container' ? 'container' : undefined,
      },
      {
        onSuccess: () => navigate('/vms'),
      },
    )
  }

  return (
    <div className="mx-auto max-w-lg">
      <h2 className="mb-6 text-xl font-semibold text-slate-800">Create VM / Container</h2>

      <form onSubmit={handleSubmit} className="space-y-5 rounded-xl border border-slate-200 bg-white p-6 shadow-sm">
        {/* Type toggle */}
        <div>
          <label className="mb-1 block text-sm font-medium text-slate-700">Type</label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setVmType('vm')}
              className={`rounded-lg px-4 py-2 text-sm font-medium ${
                vmType === 'vm'
                  ? 'bg-blue-600 text-white'
                  : 'bg-slate-100 text-slate-600 hover:bg-slate-200'
              }`}
            >
              Virtual Machine
            </button>
            <button
              type="button"
              onClick={() => setVmType('container')}
              className={`rounded-lg px-4 py-2 text-sm font-medium ${
                vmType === 'container'
                  ? 'bg-purple-600 text-white'
                  : 'bg-slate-100 text-slate-600 hover:bg-slate-200'
              }`}
            >
              Container
            </button>
          </div>
        </div>

        {/* Name */}
        <div>
          <label htmlFor="name" className="mb-1 block text-sm font-medium text-slate-700">
            Name
          </label>
          <input
            id="name"
            type="text"
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="my-workload"
            className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {/* vCPUs */}
        <div>
          <label htmlFor="vcpus" className="mb-1 block text-sm font-medium text-slate-700">
            vCPUs
          </label>
          <input
            id="vcpus"
            type="number"
            min={1}
            max={128}
            value={vcpus}
            onChange={(e) => setVcpus(Number(e.target.value))}
            className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {/* Memory */}
        <div>
          <label htmlFor="memory" className="mb-1 block text-sm font-medium text-slate-700">
            Memory (MB)
          </label>
          <input
            id="memory"
            type="number"
            min={128}
            step={128}
            value={memoryMb}
            onChange={(e) => setMemoryMb(Number(e.target.value))}
            className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>

        {/* Backend */}
        <div>
          <label htmlFor="backend" className="mb-1 block text-sm font-medium text-slate-700">
            Backend
          </label>
          <select
            id="backend"
            value={backend}
            onChange={(e) => setBackend(e.target.value)}
            className="w-full rounded-lg border border-slate-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          >
            <option value="auto">Auto (selector decides)</option>
            <option value="rustvmm">rustvmm (high-performance Linux microVM)</option>
            <option value="qemu">qemu (Windows, GPU passthrough, legacy)</option>
            {vmType === 'container' && <option value="lxc">lxc (Linux container)</option>}
          </select>
        </div>

        {/* Error message */}
        {createVM.isError && (
          <p className="text-sm text-red-600">
            Failed to create: {(createVM.error as Error).message}
          </p>
        )}

        {/* Actions */}
        <div className="flex gap-3 pt-2">
          <button
            type="submit"
            disabled={createVM.isPending || !name}
            className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
          >
            {createVM.isPending ? 'Creating...' : 'Create'}
          </button>
          <button
            type="button"
            onClick={() => navigate('/vms')}
            className="rounded-lg bg-slate-100 px-4 py-2 text-sm font-medium text-slate-600 hover:bg-slate-200"
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  )
}
