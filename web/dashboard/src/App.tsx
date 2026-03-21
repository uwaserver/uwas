import { Routes, Route, Navigate, Outlet } from 'react-router-dom';
import { getToken } from '@/lib/api';
import Sidebar from '@/components/Sidebar';
import Login from '@/pages/Login';
import Dashboard from '@/pages/Dashboard';
import Domains from '@/pages/Domains';
import Topology from '@/pages/Topology';
import Cache from '@/pages/Cache';
import Metrics from '@/pages/Metrics';
import Logs from '@/pages/Logs';
import Settings from '@/pages/Settings';

function RequireAuth() {
  if (!getToken()) {
    return <Navigate to="/login" replace />;
  }
  return (
    <div className="flex h-screen overflow-hidden bg-[#0f172a]">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-6 lg:p-8">
        <Outlet />
      </main>
    </div>
  );
}

function PublicOnly() {
  if (getToken()) {
    return <Navigate to="/" replace />;
  }
  return <Outlet />;
}

export default function App() {
  return (
    <Routes>
      {/* Public routes */}
      <Route element={<PublicOnly />}>
        <Route path="/login" element={<Login />} />
      </Route>

      {/* Authenticated routes */}
      <Route element={<RequireAuth />}>
        <Route path="/" element={<Dashboard />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="/topology" element={<Topology />} />
        <Route path="/cache" element={<Cache />} />
        <Route path="/metrics" element={<Metrics />} />
        <Route path="/logs" element={<Logs />} />
        <Route path="/settings" element={<Settings />} />
      </Route>

      {/* Fallback */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
