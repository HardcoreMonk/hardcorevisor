import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import apiClient from '../api/client'
import type {
  VMInfo, Pool, Volume, Zone, VNet, FirewallRule,
  ClusterStatus, ClusterNode, SystemStats,
  CreateVMRequest, CreateVolumeRequest, CreateZoneRequest,
  CreateBackupRequest, CreateImageRequest,
  Device, Backup, Snapshot, Template, Image, Task,
} from '../types'

// ── VM ──

export function useVMs() {
  return useQuery<VMInfo[]>({
    queryKey: ['vms'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/vms'); return data },
    refetchInterval: 5000,
  })
}

export function useCreateVM() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: CreateVMRequest) => { const { data } = await apiClient.post('/api/v1/vms', req); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

export function useVMAction() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, action }: { id: number; action: string }) => { const { data } = await apiClient.post(`/api/v1/vms/${id}/${action}`); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

export function useDeleteVM() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: number) => { await apiClient.delete(`/api/v1/vms/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

// ── System Stats ──

export function useSystemStats() {
  return useQuery<SystemStats>({
    queryKey: ['system-stats'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/system/stats'); return data },
    refetchInterval: 5000,
  })
}

// ── Storage ──

export function usePools() {
  return useQuery<Pool[]>({
    queryKey: ['pools'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/storage/pools'); return data },
  })
}

export function useVolumes() {
  return useQuery<Volume[]>({
    queryKey: ['volumes'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/storage/volumes'); return data },
  })
}

export function useCreateVolume() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: CreateVolumeRequest) => { const { data } = await apiClient.post('/api/v1/storage/volumes', req); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['volumes'] }),
  })
}

export function useDeleteVolume() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.delete(`/api/v1/storage/volumes/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['volumes'] }),
  })
}

// ── Network ──

export function useZones() {
  return useQuery<Zone[]>({
    queryKey: ['zones'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/network/zones'); return data },
  })
}

export function useVNets() {
  return useQuery<VNet[]>({
    queryKey: ['vnets'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/network/vnets'); return data },
  })
}

export function useFirewallRules() {
  return useQuery<FirewallRule[]>({
    queryKey: ['firewall'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/network/firewall'); return data },
  })
}

export function useCreateZone() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: CreateZoneRequest) => { const { data } = await apiClient.post('/api/v1/network/zones', req); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['zones'] }),
  })
}

export function useDeleteZone() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (name: string) => { await apiClient.delete(`/api/v1/network/zones/${name}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['zones'] }),
  })
}

// ── Cluster ──

export function useClusterStatus() {
  return useQuery<ClusterStatus>({
    queryKey: ['cluster-status'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/cluster/status'); return data },
    refetchInterval: 5000,
  })
}

export function useClusterNodes() {
  return useQuery<ClusterNode[]>({
    queryKey: ['cluster-nodes'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/cluster/nodes'); return data },
    refetchInterval: 5000,
  })
}

// ── Devices ──

export function useDevices() {
  return useQuery<Device[]>({
    queryKey: ['devices'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/devices'); return data },
  })
}

export function useAttachDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, vmHandle }: { id: string; vmHandle: number }) => {
      const { data } = await apiClient.post(`/api/v1/devices/${id}/attach`, { vm_handle: vmHandle })
      return data
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['devices'] }),
  })
}

export function useDetachDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.post(`/api/v1/devices/${id}/detach`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['devices'] }),
  })
}

// ── Backups ──

export function useBackups() {
  return useQuery<Backup[]>({
    queryKey: ['backups'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/backups'); return data },
  })
}

export function useCreateBackup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: CreateBackupRequest) => { const { data } = await apiClient.post('/api/v1/backups', req); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['backups'] }),
  })
}

export function useDeleteBackup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.delete(`/api/v1/backups/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['backups'] }),
  })
}

// ── Snapshots ──

export function useSnapshots() {
  return useQuery<Snapshot[]>({
    queryKey: ['snapshots'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/snapshots'); return data },
  })
}

export function useCreateSnapshot() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: { vm_id: number; vm_name: string }) => { const { data } = await apiClient.post('/api/v1/snapshots', req); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['snapshots'] }),
  })
}

export function useDeleteSnapshot() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.delete(`/api/v1/snapshots/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['snapshots'] }),
  })
}

export function useRestoreSnapshot() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { const { data } = await apiClient.post(`/api/v1/snapshots/${id}/restore`); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['snapshots'] }),
  })
}

// ── Templates ──

export function useTemplates() {
  return useQuery<Template[]>({
    queryKey: ['templates'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/templates'); return data },
  })
}

export function useDeployTemplate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { const { data } = await apiClient.post(`/api/v1/templates/${id}/deploy`); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['vms'] }),
  })
}

export function useDeleteTemplate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.delete(`/api/v1/templates/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['templates'] }),
  })
}

// ── Images ──

export function useImages() {
  return useQuery<Image[]>({
    queryKey: ['images'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/images'); return data },
  })
}

export function useCreateImage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (req: CreateImageRequest) => { const { data } = await apiClient.post('/api/v1/images', req); return data },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['images'] }),
  })
}

export function useDeleteImage() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.delete(`/api/v1/images/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['images'] }),
  })
}

// ── Tasks ──

export function useTasks() {
  return useQuery<Task[]>({
    queryKey: ['tasks'],
    queryFn: async () => { const { data } = await apiClient.get('/api/v1/tasks'); return data },
    refetchInterval: 2000,
  })
}

export function useDeleteTask() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => { await apiClient.delete(`/api/v1/tasks/${id}`) },
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks'] }),
  })
}

// ── Auth ──

export function useLogin() {
  return useMutation({
    mutationFn: async (req: { username: string; password: string }) => {
      const { data } = await apiClient.post('/api/v1/auth/login', req)
      return data
    },
  })
}

export function useLogout() {
  return useMutation({
    mutationFn: async () => {
      await apiClient.post('/api/v1/auth/logout')
      localStorage.removeItem('hcv_token')
    },
  })
}
