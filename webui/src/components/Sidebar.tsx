import { NavLink, useNavigate } from 'react-router-dom'

const navItems = [
  { to: '/dashboard', label: 'Dashboard', icon: '##' },
  { to: '/vms', label: 'VMs / Containers', icon: '[]' },
  { to: '/storage', label: 'Storage', icon: '<>' },
  { to: '/network', label: 'Network', icon: '()' },
  { to: '/devices', label: 'Devices', icon: '><' },
  { to: '/backups', label: 'Backups', icon: '=>' },
  { to: '/snapshots', label: 'Snapshots', icon: '[]' },
  { to: '/templates', label: 'Templates', icon: '::' },
  { to: '/images', label: 'Images', icon: '@@' },
  { to: '/tasks', label: 'Tasks', icon: '>>' },
  { to: '/cluster', label: 'Cluster', icon: '{}' },
]

export default function Sidebar() {
  const navigate = useNavigate()

  const handleLogout = () => {
    localStorage.removeItem('hcv_token')
    navigate('/login')
  }

  return (
    <aside className="flex h-screen w-56 flex-col bg-slate-900 text-slate-300">
      <div className="flex h-14 items-center gap-2 border-b border-slate-700 px-4">
        <span className="text-lg font-bold text-white">HardCoreVisor</span>
      </div>

      <nav className="flex-1 space-y-1 overflow-y-auto px-2 py-4">
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

      <div className="border-t border-slate-700 px-4 py-3">
        <button
          onClick={handleLogout}
          className="w-full rounded-lg px-3 py-2 text-left text-sm text-slate-400 hover:bg-slate-800 hover:text-white"
        >
          Logout
        </button>
        <p className="mt-1 text-xs text-slate-500">HardCoreVisor v0.1.0</p>
      </div>
    </aside>
  )
}
