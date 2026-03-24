import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar'

// 전체 레이아웃: 좌측 사이드바 + 우측 메인 콘텐츠 영역
// Outlet에 각 페이지 컴포넌트가 렌더링된다
export default function Layout() {
  return (
    <div className="flex h-screen bg-slate-50">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* 상단 헤더 */}
        <header className="flex h-14 items-center justify-between border-b border-slate-200 bg-white px-6">
          <h1 className="text-sm font-medium text-slate-600">Web Console</h1>
          <div className="flex items-center gap-4">
            <span className="text-xs text-slate-400">Controller: localhost:8080</span>
          </div>
        </header>

        {/* 메인 콘텐츠 */}
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
