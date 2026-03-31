import { useTasks, useDeleteTask } from '../hooks/useApi'

export default function TasksPage() {
  const { data: tasks, isLoading } = useTasks()
  const deleteTask = useDeleteTask()

  if (isLoading) return <p className="p-6 text-slate-400">Loading tasks...</p>

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-bold text-white">Async Tasks</h1>
      <div className="overflow-x-auto rounded-lg border border-slate-700">
        <table className="w-full text-left text-sm text-slate-300">
          <thead className="bg-slate-800 text-xs uppercase text-slate-400">
            <tr>
              <th className="px-4 py-3">ID</th>
              <th className="px-4 py-3">Type</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Progress</th>
              <th className="px-4 py-3">Created</th>
              <th className="px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-700">
            {(tasks || []).map((t: any) => (
              <tr key={t.id} className="hover:bg-slate-800/50">
                <td className="px-4 py-3 font-mono text-xs">{t.id}</td>
                <td className="px-4 py-3">{t.type}</td>
                <td className="px-4 py-3">
                  <span className={`rounded px-2 py-0.5 text-xs font-medium ${
                    t.status === 'completed' ? 'bg-green-900 text-green-300' :
                    t.status === 'failed' ? 'bg-red-900 text-red-300' :
                    t.status === 'running' ? 'bg-blue-900 text-blue-300' :
                    'bg-slate-700 text-slate-300'
                  }`}>{t.status}</span>
                </td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <div className="h-2 w-24 rounded-full bg-slate-700">
                      <div className="h-2 rounded-full bg-blue-500" style={{ width: `${t.progress}%` }} />
                    </div>
                    <span className="text-xs">{t.progress}%</span>
                  </div>
                </td>
                <td className="px-4 py-3 text-xs">{t.created_at}</td>
                <td className="px-4 py-3">
                  {(t.status === 'completed' || t.status === 'failed') && (
                    <button onClick={() => deleteTask.mutate(t.id)}
                      className="rounded bg-red-600 px-2 py-1 text-xs text-white hover:bg-red-700">Remove</button>
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
