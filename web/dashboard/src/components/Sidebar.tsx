import { useState } from 'react';
import { NavLink, useNavigate } from 'react-router-dom';
import {
  LayoutDashboard,
  Globe,
  Network,
  Database,
  BarChart3,
  FileText,
  Settings,
  Menu,
  X,
  LogOut,
  TrendingUp,
  Code,
  Lock,
  Cpu,
  Archive,
  HardDrive,
  Waypoints,
  Shield,
  ShieldAlert,
  Zap,
  Mail,
  Users,
  FolderOpen,
  Clock,
  ShieldCheck,
  Download,
  Server,
} from 'lucide-react';
import { clearToken } from '@/lib/api';

const links = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/domains', label: 'Domains', icon: Globe },
  { to: '/topology', label: 'Topology', icon: Network },
  { to: '/cache', label: 'Cache', icon: Database },
  { to: '/metrics', label: 'Metrics', icon: BarChart3 },
  { to: '/analytics', label: 'Analytics', icon: TrendingUp },
  { to: '/logs', label: 'Logs', icon: FileText },
  { to: '/config-editor', label: 'Config Editor', icon: Code },
  { to: '/certificates', label: 'Certificates', icon: Lock },
  { to: '/dns', label: 'DNS Checker', icon: Waypoints },
  { to: '/ip-management', label: 'IP Management', icon: Server },
  { to: '/email', label: 'Email Guide', icon: Mail },
  { to: '/php', label: 'PHP', icon: Cpu },
  { to: '/php-config', label: 'PHP Config', icon: Settings },
  { to: '/backups', label: 'Backups', icon: Archive },
  { to: '/database', label: 'Database', icon: HardDrive },
  { to: '/wordpress', label: 'WordPress', icon: Zap },
  { to: '/users', label: 'SFTP Users', icon: Users },
  { to: '/file-manager', label: 'File Manager', icon: FolderOpen },
  { to: '/cron', label: 'Cron Jobs', icon: Clock },
  { to: '/firewall', label: 'Firewall', icon: ShieldCheck },
  { to: '/security', label: 'Security', icon: Shield },
  { to: '/unknown-domains', label: 'Unknown Domains', icon: ShieldAlert },
  { to: '/audit', label: 'Audit Log', icon: Shield },
  { to: '/updates', label: 'Updates', icon: Download },
  { to: '/settings', label: 'Settings', icon: Settings },
];

export default function Sidebar() {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();

  const handleLogout = () => {
    clearToken();
    navigate('/login');
  };

  const nav = (
    <nav className="flex flex-1 flex-col gap-1 px-3 py-4">
      {links.map(({ to, label, icon: Icon }) => (
        <NavLink
          key={to}
          to={to}
          end={to === '/'}
          onClick={() => setOpen(false)}
          className={({ isActive }) =>
            `flex items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium transition-colors ${
              isActive
                ? 'bg-blue-600/20 text-blue-400'
                : 'text-slate-400 hover:bg-[#334155] hover:text-slate-200'
            }`
          }
        >
          <Icon size={18} />
          {label}
        </NavLink>
      ))}
    </nav>
  );

  return (
    <>
      {/* Mobile toggle */}
      <button
        onClick={() => setOpen(!open)}
        className="fixed top-4 left-4 z-50 rounded-md bg-[#1e293b] p-2 text-slate-300 shadow-lg lg:hidden"
        aria-label="Toggle menu"
      >
        {open ? <X size={20} /> : <Menu size={20} />}
      </button>

      {/* Backdrop */}
      {open && (
        <div
          className="fixed inset-0 z-30 bg-black/50 lg:hidden"
          onClick={() => setOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside
        className={`fixed top-0 left-0 z-40 flex h-full w-60 flex-col border-r border-[#334155] bg-[#0f172a] transition-transform lg:static lg:translate-x-0 ${
          open ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        {/* Logo */}
        <div className="flex items-center gap-2.5 border-b border-[#334155] px-5 py-5">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-600 text-sm font-bold text-white">
            U
          </div>
          <span className="text-lg font-bold tracking-tight text-slate-100">
            UWAS
          </span>
        </div>

        {nav}

        {/* Logout */}
        <div className="border-t border-[#334155] px-3 py-4">
          <button
            onClick={handleLogout}
            className="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium text-slate-400 transition-colors hover:bg-[#334155] hover:text-slate-200"
          >
            <LogOut size={18} />
            Logout
          </button>
        </div>
      </aside>
    </>
  );
}
