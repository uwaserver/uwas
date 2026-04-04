import { useState, useEffect, lazy, Suspense } from 'react';
import { Routes, Route, Navigate, Outlet } from 'react-router-dom';
import { getToken, onPinRequired } from '@/lib/api';
import Sidebar from '@/components/Sidebar';
import PinModal from '@/components/PinModal';
import Login from '@/pages/Login';

const Dashboard = lazy(() => import('@/pages/Dashboard'));
const Domains = lazy(() => import('@/pages/Domains'));
const Topology = lazy(() => import('@/pages/Topology'));
const Cache = lazy(() => import('@/pages/Cache'));
const Metrics = lazy(() => import('@/pages/Metrics'));
const Logs = lazy(() => import('@/pages/Logs'));
const Settings = lazy(() => import('@/pages/Settings'));
const Analytics = lazy(() => import('@/pages/Analytics'));
const ConfigEditor = lazy(() => import('@/pages/ConfigEditor'));
const Certificates = lazy(() => import('@/pages/Certificates'));
const PHP = lazy(() => import('@/pages/PHP'));
const Backups = lazy(() => import('@/pages/Backups'));
const Database = lazy(() => import('@/pages/Database'));
const DBExplorer = lazy(() => import('@/pages/DBExplorer'));
const DNS = lazy(() => import('@/pages/DNS'));
const AuditLog = lazy(() => import('@/pages/AuditLog'));
const UnknownDomains = lazy(() => import('@/pages/UnknownDomains'));
const Users = lazy(() => import('@/pages/Users'));
const FileManager = lazy(() => import('@/pages/FileManager'));
const CronJobs = lazy(() => import('@/pages/CronJobs'));
const Firewall = lazy(() => import('@/pages/Firewall'));
const Updates = lazy(() => import('@/pages/Updates'));
const Security = lazy(() => import('@/pages/Security'));
const EmailGuide = lazy(() => import('@/pages/EmailGuide'));
const WordPress = lazy(() => import('@/pages/WordPress'));
const PHPConfig = lazy(() => import('@/pages/PHPConfig'));
const IPManagement = lazy(() => import('@/pages/IPManagement'));
const Services = lazy(() => import('@/pages/Services'));
const Packages = lazy(() => import('@/pages/Packages'));
const Doctor = lazy(() => import('@/pages/Doctor'));
const CloneStaging = lazy(() => import('@/pages/CloneStaging'));
const Migration = lazy(() => import('@/pages/Migration'));
const Webhooks = lazy(() => import('@/pages/Webhooks'));
const AdminUsers = lazy(() => import('@/pages/AdminUsers'));
const DomainDetail = lazy(() => import('@/pages/DomainDetail'));
const About = lazy(() => import('@/pages/About'));
const Apps = lazy(() => import('@/pages/Apps'));
const TerminalPage = lazy(() => import('@/pages/Terminal'));
const Cloudflare = lazy(() => import('@/pages/Cloudflare'));

function PageLoader() {
  return (
    <div className="flex items-center justify-center h-64">
      <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary" />
    </div>
  );
}

function RequireAuth() {
  if (!getToken()) {
    return <Navigate to="/login" replace />;
  }
  return (
    <div className="flex h-screen overflow-hidden bg-background">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-4 pt-16 sm:p-6 sm:pt-6 lg:p-8">
        <Suspense fallback={<PageLoader />}>
          <Outlet />
        </Suspense>
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
  const [pinOpen, setPinOpen] = useState(false);
  const [pinResolver, setPinResolver] = useState<{ resolve: (pin: string) => void; reject: () => void } | null>(null);

  useEffect(() => {
    onPinRequired((resolve, reject) => {
      setPinResolver({ resolve, reject });
      setPinOpen(true);
    });

    // Apply white-label branding (favicon + document title)
    import('@/lib/api').then(({ fetchBranding }) => {
      fetchBranding().then(b => {
        if (b.favicon_url) {
          const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]') || document.createElement('link');
          link.rel = 'icon';
          link.href = b.favicon_url;
          document.head.appendChild(link);
        }
        if (b.name) {
          document.title = b.name;
        }
      }).catch(() => {});
    });
  }, []);

  return (
    <>
    <PinModal
      open={pinOpen}
      onConfirm={(pin) => {
        setPinOpen(false);
        pinResolver?.resolve(pin);
        setPinResolver(null);
      }}
      onCancel={() => {
        setPinOpen(false);
        pinResolver?.reject();
        setPinResolver(null);
      }}
    />
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
        <Route path="/db-explorer" element={<DBExplorer />} />
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
        <Route path="/apps" element={<Apps />} />
        <Route path="/terminal" element={<TerminalPage />} />
        <Route path="/cloudflare" element={<Cloudflare />} />
        <Route path="/about" element={<About />} />
      </Route>

      {/* Fallback */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
    </>
  );
}
