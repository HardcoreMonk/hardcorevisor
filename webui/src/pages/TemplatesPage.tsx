import { useTemplates, useDeployTemplate, useDeleteTemplate } from '../hooks/useApi'

export default function TemplatesPage() {
  const { data: templates, isLoading } = useTemplates()
  const deploy = useDeployTemplate()
  const deleteTpl = useDeleteTemplate()

  if (isLoading) return <p className="p-6 text-slate-400">Loading templates...</p>

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold text-white">VM Templates</h1>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {(templates || []).map((t: any) => (
          <div key={t.id} className="rounded-lg border border-slate-700 bg-slate-800 p-4">
            <h3 className="text-lg font-semibold text-white">{t.name}</h3>
            <div className="mt-2 space-y-1 text-sm text-slate-400">
              <p>vCPUs: {t.vcpus} / Memory: {t.memory_mb} MB</p>
              <p>Backend: {t.backend || 'auto'}</p>
              {t.description && <p className="text-xs">{t.description}</p>}
            </div>
            <div className="mt-3 flex gap-2">
              <button onClick={() => deploy.mutate(t.id)}
                className="rounded bg-green-600 px-3 py-1 text-sm text-white hover:bg-green-700">
                Deploy
              </button>
              <button onClick={() => deleteTpl.mutate(t.id)}
                className="rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700">
                Delete
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
