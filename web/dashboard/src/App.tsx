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
import Analytics from '@/pages/Analytics';
import ConfigEditor from '@/pages/ConfigEditor';
import Certificates from '@/pages/Certificates';
import PHP from '@/pages/PHP';
import Backups from '@/pages/Backups';
import Database from '@/pages/Database';
import DNS from '@/pages/DNS';
import AuditLog from '@/pages/AuditLog';
import UnknownDomains from '@/pages/UnknownDomains';
import Users from '@/pages/Users';
import FileManager from '@/pages/FileManager';
import CronJobs from '@/pages/CronJobs';
import Firewall from '@/pages/Firewall';
import Updates from '@/pages/Updates';
import Security from '@/pages/Security';
import EmailGuide from '@/pages/EmailGuide';
import WordPress from '@/pages/WordPress';
import PHPConfig from '@/pages/PHPConfig';
import IPManagement from '@/pages/IPManagement';
import Services from '@/pages/Services';
import Packages from '@/pages/Packages';
import Doctor from '@/pages/Doctor';
import CloneStaging from '@/pages/CloneStaging';
import Migration from '@/pages/Migration';
import Webhooks from '@/pages/Webhooks';
import AdminUsers from '@/pages/AdminUsers';
import DomainDetail from '@/pages/DomainDetail';
import About from '@/pages/About';

function RequireAuth() {
  if (!getToken()) {
    return <Navigate to="/login" replace />;
  }
  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-4 pt-16 sm:p-6 sm:pt-6 lg:p-8">
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
        <Route path="/domains/:host" element={<DomainDetail />} />
        <Route path="/topology" element={<Topology />} />
        <Route path="/cache" element={<Cache />} />
        <Route path="/metrics" element={<Metrics />} />
        <Route path="/logs" element={<Logs />} />
        <Route path="/analytics" element={<Analytics />} />
        <Route path="/config-editor" element={<ConfigEditor />} />
        <Route path="/certificates" element={<Certificates />} />
        <Route path="/php" element={<PHP />} />
        <Route path="/php-config" element={<PHPConfig />} />
        <Route path="/backups" element={<Backups />} />
        <Route path="/database" element={<Database />} />
        <Route path="/wordpress" element={<WordPress />} />
        <Route path="/clone" element={<CloneStaging />} />
        <Route path="/migrate" element={<Migration />} />
        <Route path="/webhooks" element={<Webhooks />} />
        <Route path="/admin-users" element={<AdminUsers />} />
        <Route path="/dns" element={<DNS />} />
        <Route path="/ip-management" element={<IPManagement />} />
        <Route path="/audit" element={<AuditLog />} />
        <Route path="/unknown-domains" element={<UnknownDomains />} />
        <Route path="/users" element={<Users />} />
        <Route path="/file-manager" element={<FileManager />} />
        <Route path="/cron" element={<CronJobs />} />
        <Route path="/firewall" element={<Firewall />} />
        <Route path="/security" element={<Security />} />
        <Route path="/email" element={<EmailGuide />} />
        <Route path="/updates" element={<Updates />} />
        <Route path="/services" element={<Services />} />
        <Route path="/packages" element={<Packages />} />
        <Route path="/doctor" element={<Doctor />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/about" element={<About />} />
      </Route>

      {/* Fallback */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
