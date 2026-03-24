import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import apiClient from '../api/client'
import type {
  VMInfo,
  Pool,
  Volume,
  Zone,
  VNet,
  FirewallRule,
  ClusterStatus,
  ClusterNode,
  SystemStats,
  CreateVMRequest,
} from '../types'

// React Query 훅 모음
// 각 훅은 Controller REST API 엔드포인트에 대응한다

// ── VM 관련 ──

/** VM 목록 조회 (5초 자동 갱신) */
export function useVMs() {
  return useQuery<VMInfo[]>({
    queryKey: ['vms'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/vms')
      return data
    },
    refetchInterval: 5000,
  })
}

/** VM 생성 뮤테이션 */
export function useCreateVM() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: CreateVMRequest) => {
      const { data } = await apiClient.post('/api/v1/vms', req)
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

/** VM 액션 (start, stop, pause, resume) */
export function useVMAction() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, action }: { id: number; action: string }) => {
      const { data } = await apiClient.post(`/api/v1/vms/${id}/${action}`)
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

/** VM 삭제 */
export function useDeleteVM() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: number) => {
      await apiClient.delete(`/api/v1/vms/${id}`)
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

// ── 시스템 통계 ──

/** 시스템 통계 (대시보드용, 5초 자동 갱신) */
export function useSystemStats() {
  return useQuery<SystemStats>({
    queryKey: ['system-stats'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/system/stats')
      return data
    },
    refetchInterval: 5000,
  })
}

// ── 스토리지 ──

/** 스토리지 풀 목록 */
export function usePools() {
  return useQuery<Pool[]>({
    queryKey: ['pools'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/storage/pools')
      return data
    },
  })
}

/** 스토리지 볼륨 목록 */
export function useVolumes() {
  return useQuery<Volume[]>({
    queryKey: ['volumes'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/storage/volumes')
      return data
    },
  })
}

// ── 네트워크 ──

/** SDN 존 목록 */
export function useZones() {
  return useQuery<Zone[]>({
    queryKey: ['zones'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/network/zones')
      return data
    },
  })
}

/** 가상 네트워크 목록 */
export function useVNets() {
  return useQuery<VNet[]>({
    queryKey: ['vnets'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/network/vnets')
      return data
    },
  })
}

/** 방화벽 규칙 목록 */
export function useFirewallRules() {
  return useQuery<FirewallRule[]>({
    queryKey: ['firewall'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/network/firewall')
      return data
    },
  })
}

// ── 클러스터 ──

/** 클러스터 상태 */
export function useClusterStatus() {
  return useQuery<ClusterStatus>({
    queryKey: ['cluster-status'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/cluster/status')
      return data
    },
    refetchInterval: 5000,
  })
}

/** 클러스터 노드 목록 */
export function useClusterNodes() {
  return useQuery<ClusterNode[]>({
    queryKey: ['cluster-nodes'],
    queryFn: async () => {
      const { data } = await apiClient.get('/api/v1/cluster/nodes')
      return data
    },
    refetchInterval: 5000,
  })
}
