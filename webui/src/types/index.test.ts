import { describe, it, expect } from 'vitest'
import type { VMInfo, CreateVMRequest, Pool, ClusterStatus } from './index'

describe('Type definitions', () => {
  it('VMInfo has required fields', () => {
    const vm: VMInfo = {
      handle: 1, name: 'test', vcpus: 2, memory_mb: 4096,
      state: 'running', backend: 'rustvmm', type: 'vm',
      node: 'node-01', restart_policy: 'always',
      ip_address: '10.0.0.1', template: '',
    }
    expect(vm.handle).toBe(1)
    expect(vm.state).toBe('running')
  })

  it('CreateVMRequest has optional backend', () => {
    const req: CreateVMRequest = { name: 'test', vcpus: 1, memory_mb: 256 }
    expect(req.backend).toBeUndefined()
  })

  it('Pool has usage fields', () => {
    const pool: Pool = {
      name: 'local-zfs', pool_type: 'zfs',
      total_bytes: 1e12, used_bytes: 5e11, health: 'ONLINE',
    }
    expect(pool.total_bytes).toBeGreaterThan(pool.used_bytes)
  })

  it('ClusterStatus has quorum', () => {
    const status: ClusterStatus = {
      quorum: true, node_count: 3, online_count: 3,
      leader: 'node-01', health: 'healthy',
    }
    expect(status.quorum).toBe(true)
  })
})
