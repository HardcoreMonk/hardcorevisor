import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'

export default function VmConsole() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const canvasRef = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting')
  const [error, setError] = useState('')

  useEffect(() => {
    if (!id || !canvasRef.current) return

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/api/v1/vms/${id}/console`

    // noVNC는 CDN에서 로드 (또는 npm install @novnc/novnc)
    // 여기서는 순수 WebSocket으로 연결 상태만 표시
    const ws = new WebSocket(wsUrl)

    ws.onopen = () => {
      setStatus('connected')
    }

    ws.onclose = () => {
      setStatus('disconnected')
    }

    ws.onerror = () => {
      setError('VNC connection failed. Is the VM running in QEMU Real mode?')
      setStatus('disconnected')
    }

    return () => {
      ws.close()
    }
  }, [id])

  return (
    <div className="flex h-full flex-col p-6">
      <div className="mb-4 flex items-center justify-between">
        <div className="flex items-center gap-4">
          <button
            onClick={() => navigate('/vms')}
            className="rounded bg-slate-700 px-3 py-1 text-sm text-white hover:bg-slate-600"
          >
            Back
          </button>
          <h1 className="text-xl font-bold text-white">VM Console — #{id}</h1>
        </div>
        <span className={`rounded px-2 py-1 text-xs font-medium ${
          status === 'connected' ? 'bg-green-900 text-green-300' :
          status === 'connecting' ? 'bg-yellow-900 text-yellow-300' :
          'bg-red-900 text-red-300'
        }`}>{status}</span>
      </div>

      {error && <p className="mb-4 text-sm text-red-400">{error}</p>}

      <div
        ref={canvasRef}
        className="flex flex-1 items-center justify-center rounded-lg border border-slate-700 bg-black"
      >
        {status === 'connecting' && (
          <p className="text-slate-400">Connecting to VNC console...</p>
        )}
        {status === 'connected' && (
          <p className="text-slate-400">
            VNC connected. For full noVNC rendering, install @novnc/novnc package.
          </p>
        )}
        {status === 'disconnected' && !error && (
          <p className="text-slate-400">Console disconnected.</p>
        )}
      </div>

      <div className="mt-4 text-xs text-slate-500">
        VNC over WebSocket proxy. Requires QEMU Real mode (HCV_QEMU_MODE=real).
      </div>
    </div>
  )
}
