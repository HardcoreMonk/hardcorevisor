import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import LoginPage from './pages/LoginPage'
import Dashboard from './pages/Dashboard'
import VmList from './pages/VmList'
import VmCreate from './pages/VmCreate'
import StoragePage from './pages/StoragePage'
import NetworkPage from './pages/NetworkPage'
import ClusterPage from './pages/ClusterPage'
import DevicesPage from './pages/DevicesPage'
import BackupsPage from './pages/BackupsPage'
import SnapshotsPage from './pages/SnapshotsPage'
import TemplatesPage from './pages/TemplatesPage'
import ImagesPage from './pages/ImagesPage'
import TasksPage from './pages/TasksPage'
import { useWebSocket } from './hooks/useWebSocket'

function AuthGuard({ children }: { children: React.ReactNode }) {
  const token = localStorage.getItem('hcv_token')
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

function AppRoutes() {
  // WebSocket 실시간 이벤트 구독 (앱 레벨)
  useWebSocket()

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<AuthGuard><Layout /></AuthGuard>}>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/vms" element={<VmList />} />
        <Route path="/vms/create" element={<VmCreate />} />
        <Route path="/storage" element={<StoragePage />} />
        <Route path="/network" element={<NetworkPage />} />
        <Route path="/cluster" element={<ClusterPage />} />
        <Route path="/devices" element={<DevicesPage />} />
        <Route path="/backups" element={<BackupsPage />} />
        <Route path="/snapshots" element={<SnapshotsPage />} />
        <Route path="/templates" element={<TemplatesPage />} />
        <Route path="/images" element={<ImagesPage />} />
        <Route path="/tasks" element={<TasksPage />} />
      </Route>
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  )
}

export default App
