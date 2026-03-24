// Go Controller 구조체와 일치하는 TypeScript 인터페이스 정의

export interface VMInfo {
  handle: number
  name: string
  vcpus: number
  memory_mb: number
  state: string
  backend: string
  type: string
  node: string
  restart_policy: string
  ip_address: string
  template: string
}

export interface Pool {
  name: string
  pool_type: string
  total_bytes: number
  used_bytes: number
  health: string
}

export interface Volume {
  id: string
  pool: string
  name: string
  size_bytes: number
  format: string
}

export interface Zone {
  name: string
  type: string
  bridge: string
  mtu: number
}

export interface VNet {
  name: string
  zone: string
  tag: number
  subnet: string
  gateway: string
}

export interface FirewallRule {
  id: string
  direction: string
  action: string
  proto: string
  dport: string
  source: string
  comment: string
}

export interface ClusterStatus {
  quorum: boolean
  node_count: number
  online_count: number
  leader: string
  health: string
}

export interface ClusterNode {
  id: string
  name: string
  address: string
  status: string
  role: string
  vcpus_total: number
  vcpus_used: number
  memory_total_mb: number
  memory_used_mb: number
}

export interface SystemStats {
  total_vms: number
  running_vms: number
  stopped_vms: number
  total_nodes: number
  online_nodes: number
  storage_pools: number
  total_storage_bytes: number
  used_storage_bytes: number
  uptime_seconds: number
}

export interface Task {
  id: string
  type: string
  status: string
  progress: number
  created_at: string
}

export interface VersionInfo {
  product: string
  version: string
  vmcore_version: string
  go_version: string
  api_version: string
}

export interface CreateVMRequest {
  name: string
  vcpus: number
  memory_mb: number
  backend?: string
  type?: string
}
