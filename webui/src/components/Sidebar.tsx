import { NavLink } from 'react-router-dom'

// 사이드바 네비게이션 항목 정의
const navItems = [
  { to: '/dashboard', label: 'Dashboard', icon: '##' },
  { to: '/vms', label: 'VMs / Containers', icon: '[]' },
  { to: '/storage', label: 'Storage', icon: '<>' },
  { to: '/network', label: 'Network', icon: '()' },
  { to: '/cluster', label: 'Cluster', icon: '{}' },
]

export default function Sidebar() {
  return (
    <aside className="flex h-screen w-56 flex-col bg-slate-900 text-slate-300">
      {/* 로고 영역 */}
      <div className="flex h-14 items-center gap-2 border-b border-slate-700 px-4">
        <span className="text-lg font-bold text-white">HardCoreVisor</span>
      </div>

      {/* 네비게이션 메뉴 */}
      <nav className="flex-1 space-y-1 px-2 py-4">
        {navItems.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) =>
              `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                isActive
                  ? 'bg-slate-800 text-white'
                  : 'text-slate-400 hover:bg-slate-800 hover:text-white'
              }`
            }
          >
            <span className="font-mono text-xs text-slate-500">{item.icon}</span>
            {item.label}
          </NavLink>
        ))}
      </nav>

      {/* 하단 버전 정보 */}
      <div className="border-t border-slate-700 px-4 py-3 text-xs text-slate-500">
        HardCoreVisor v0.1.0
      </div>
    </aside>
  )
}
