import { useEffect, useRef, useState, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'

type ConnStatus = 'connecting' | 'connected' | 'disconnected'

/** WebSocket 실시간 이벤트 구독 훅.
 *  /ws 엔드포인트에 연결하여 VM 상태 변경, 생성, 삭제 이벤트를 수신하고
 *  TanStack Query 캐시를 자동으로 무효화한다.
 */
export function useWebSocket() {
  const qc = useQueryClient()
  const wsRef = useRef<WebSocket | null>(null)
  const [status, setStatus] = useState<ConnStatus>('disconnected')
  const retryRef = useRef(0)
  const maxRetries = 10

  const connect = useCallback(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws`

    try {
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws
      setStatus('connecting')

      ws.onopen = () => {
        setStatus('connected')
        retryRef.current = 0
      }

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          // VM 관련 이벤트 시 캐시 무효화
          if (msg.type?.startsWith('vm_') || msg.type === 'container_') {
            qc.invalidateQueries({ queryKey: ['vms'] })
            qc.invalidateQueries({ queryKey: ['system-stats'] })
          }
          if (msg.type?.startsWith('storage_')) {
            qc.invalidateQueries({ queryKey: ['volumes'] })
            qc.invalidateQueries({ queryKey: ['pools'] })
          }
          if (msg.type?.startsWith('node_') || msg.type?.startsWith('cluster_')) {
            qc.invalidateQueries({ queryKey: ['cluster-status'] })
            qc.invalidateQueries({ queryKey: ['cluster-nodes'] })
          }
          if (msg.type?.startsWith('task_')) {
            qc.invalidateQueries({ queryKey: ['tasks'] })
          }
        } catch { /* 파싱 실패 무시 */ }
      }

      ws.onclose = () => {
        setStatus('disconnected')
        wsRef.current = null
        // 지수 백오프 재연결
        if (retryRef.current < maxRetries) {
          const delay = Math.min(1000 * Math.pow(2, retryRef.current), 30000)
          retryRef.current++
          setTimeout(connect, delay)
        }
      }

      ws.onerror = () => {
        ws.close()
      }
    } catch {
      setStatus('disconnected')
    }
  }, [qc])

  useEffect(() => {
    connect()
    return () => {
      wsRef.current?.close()
    }
  }, [connect])

  return { status }
}
