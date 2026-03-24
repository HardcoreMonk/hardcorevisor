interface StatusBadgeProps {
  state: string
}

const stateStyles: Record<string, string> = {
  running: 'bg-green-100 text-green-800',
  stopped: 'bg-gray-100 text-gray-800',
  paused: 'bg-yellow-100 text-yellow-800',
  created: 'bg-blue-100 text-blue-800',
  configured: 'bg-indigo-100 text-indigo-800',
  error: 'bg-red-100 text-red-800',
}

export default function StatusBadge({ state }: StatusBadgeProps) {
  const style = stateStyles[state.toLowerCase()] ?? 'bg-gray-100 text-gray-600'
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${style}`}>
      {state}
    </span>
  )
}
