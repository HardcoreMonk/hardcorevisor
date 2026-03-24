import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import VmList from './pages/VmList'
import VmCreate from './pages/VmCreate'
import StoragePage from './pages/StoragePage'
import NetworkPage from './pages/NetworkPage'
import ClusterPage from './pages/ClusterPage'

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/vms" element={<VmList />} />
          <Route path="/vms/create" element={<VmCreate />} />
          <Route path="/storage" element={<StoragePage />} />
          <Route path="/network" element={<NetworkPage />} />
          <Route path="/cluster" element={<ClusterPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
