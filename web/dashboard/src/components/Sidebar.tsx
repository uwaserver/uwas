import { useState, useEffect } from 'react';
import { NavLink, useNavigate, useLocation } from 'react-router-dom';
import { fetchSystem, fetchBranding, type BrandingConfig } from '@/lib/api';
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
  Activity,
  ChevronDown,
  ChevronRight,
  Stethoscope,
  Package,
  Sun,
  Moon,
  Copy,
  ArrowDownToLine,
  Webhook,
  UserCog,
  Info,
  Terminal,
  Box,
  Cloud,
} from 'lucide-react';
import { clearToken } from '@/lib/api';
import { useTheme } from '@/hooks/useTheme';

interface NavItem {
  to: string;
  label: string;
  icon: React.ComponentType<{ size?: number }>;
}

interface NavGroup {
  label: string;
  icon: React.ComponentType<{ size?: number }>;
  items: NavItem[];
}

const topLinks: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
];

const groups: NavGroup[] = [
  {
    label: 'Sites',
    icon: Globe,
    items: [
      { to: '/domains', label: 'Domains', icon: Globe },
      { to: '/topology', label: 'Topology', icon: Network },
      { to: '/certificates', label: 'Certificates', icon: Lock },
      { to: '/dns', label: 'DNS', icon: Waypoints },
      { to: '/cloudflare', label: 'Cloudflare', icon: Cloud },
      { to: '/wordpress', label: 'WordPress', icon: Zap },
      { to: '/clone', label: 'Clone / Staging', icon: Copy },
      { to: '/migrate', label: 'Migration', icon: ArrowDownToLine },
      { to: '/file-manager', label: 'File Manager', icon: FolderOpen },
    ],
  },
  {
    label: 'Server',
    icon: Server,
    items: [
      { to: '/php', label: 'PHP', icon: Cpu },
      { to: '/php-config', label: 'PHP Config', icon: Settings },
      { to: '/apps', label: 'Applications', icon: Box },
      { to: '/database', label: 'Database', icon: HardDrive },
      { to: '/db-explorer', label: 'DB Explorer', icon: Database },
      { to: '/users', label: 'SFTP Users', icon: Users },
      { to: '/cron', label: 'Cron Jobs', icon: Clock },
      { to: '/services', label: 'Services', icon: Activity },
      { to: '/packages', label: 'Packages', icon: Package },
      { to: '/ip-management', label: 'IP Management', icon: Server },
      { to: '/email', label: 'Email Guide', icon: Mail },
    ],
  },
  {
    label: 'Performance',
    icon: BarChart3,
    items: [
      { to: '/cache', label: 'Cache', icon: Database },
      { to: '/metrics', label: 'Metrics', icon: BarChart3 },
      { to: '/analytics', label: 'Analytics', icon: TrendingUp },
      { to: '/logs', label: 'Logs', icon: FileText },
    ],
  },
  {
    label: 'Security',
    icon: Shield,
    items: [
      { to: '/security', label: 'Security', icon: Shield },
      { to: '/firewall', label: 'Firewall', icon: ShieldCheck },
      { to: '/unknown-domains', label: 'Unknown Domains', icon: ShieldAlert },
      { to: '/audit', label: 'Audit Log', icon: Shield },
      { to: '/admin-users', label: 'Admin Users', icon: UserCog },
    ],
  },
  {
    label: 'System',
    icon: Settings,
    items: [
      { to: '/config-editor', label: 'Config Editor', icon: Code },
      { to: '/webhooks', label: 'Webhooks', icon: Webhook },
      { to: '/backups', label: 'Backups', icon: Archive },
      { to: '/terminal', label: 'Terminal', icon: Terminal },
      { to: '/updates', label: 'Updates', icon: Download },
      { to: '/doctor', label: 'Doctor', icon: Stethoscope },
      { to: '/settings', label: 'Settings', icon: Settings },
      { to: '/about', label: 'About', icon: Info },
    ],
  },
];

function NavLinkItem({ to, label, icon: Icon, onClick }: NavItem & { onClick?: () => void }) {
  return (
    <NavLink
      to={to}
      end={to === '/'}
      onClick={onClick}
      className={({ isActive }) =>
        `flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
          isActive
            ? 'bg-blue-600/20 text-blue-400'
            : 'text-muted-foreground hover:bg-accent hover:text-foreground'
        }`
      }
    >
      <Icon size={16} />
      {label}
    </NavLink>
  );
}

function CollapsibleGroup({ group, onNavClick }: { group: NavGroup; onNavClick?: () => void }) {
  const location = useLocation();
  const isActiveGroup = group.items.some(item =>
    item.to === '/' ? location.pathname === '/' : location.pathname.startsWith(item.to)
  );
  const [open, setOpen] = useState(isActiveGroup);
  const Icon = group.icon;

  return (
    <div className="mb-1">
      <button
        onClick={() => setOpen(!open)}
        className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
          isActiveGroup
            ? 'text-foreground'
            : 'text-muted-foreground hover:text-foreground'
        }`}
      >
        <Icon size={16} />
        <span className="flex-1 text-left">{group.label}</span>
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
      </button>
      {open && (
        <div className="ml-3 flex flex-col gap-0.5 border-l border-border pl-2">
          {group.items.map(item => (
            <NavLinkItem key={item.to} {...item} onClick={onNavClick} />
          ))}
        </div>
      )}
    </div>
  );
}

export default function Sidebar() {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [version, setVersion] = useState('');
  const [branding, setBranding] = useState<BrandingConfig>({});
  const navigate = useNavigate();
  const { theme, toggle } = useTheme();

  useEffect(() => {
    fetchSystem().then(s => setVersion(s?.version || '')).catch(() => {});
    fetchBranding().then(setBranding).catch(() => {});
  }, []);

  const handleLogout = () => {
    clearToken();
    navigate('/login');
  };

  const closeMobile = () => setMobileOpen(false);

  const nav = (
    <nav className="flex flex-1 flex-col gap-0.5 overflow-y-auto px-3 py-4">
      {topLinks.map(item => (
        <NavLinkItem key={item.to} {...item} onClick={closeMobile} />
      ))}

      <div className="my-2 border-t border-border/50" />

      {groups.map(group => (
        <CollapsibleGroup key={group.label} group={group} onNavClick={closeMobile} />
      ))}
    </nav>
  );

  return (
    <>
      {/* Mobile toggle */}
      <button
        onClick={() => setMobileOpen(!mobileOpen)}
        className="fixed top-4 left-4 z-50 rounded-md bg-card p-2 text-muted-foreground shadow-lg lg:hidden"
        aria-label="Toggle menu"
      >
        {mobileOpen ? <X size={20} /> : <Menu size={20} />}
      </button>

      {/* Backdrop */}
      {mobileOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50 lg:hidden"
          onClick={closeMobile}
        />
      )}

      {/* Sidebar */}
      <aside
        className={`fixed top-0 left-0 z-40 flex h-full w-60 flex-col border-r border-border bg-background transition-transform lg:static lg:translate-x-0 ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        {/* Logo */}
        <div className="flex items-center gap-2.5 border-b border-border px-5 py-5">
          {branding.logo_url ? (
            <img src={branding.logo_url} alt={branding.name || 'UWAS'} className="h-8" />
          ) : (
            <div className="flex h-8 w-8 items-center justify-center rounded-lg text-sm font-bold text-white"
              style={{ backgroundColor: branding.primary_color || '#2563eb' }}>
              {(branding.name || 'UWAS')[0]}
            </div>
          )}
          <span className="text-lg font-bold tracking-tight text-foreground">
            {branding.name || 'UWAS'}
          </span>
          <span className="ml-auto rounded bg-blue-600/20 px-1.5 py-0.5 text-[10px] font-medium text-blue-400">
            {version || '...'}
          </span>
        </div>

        {nav}

        {/* Theme toggle + Logout */}
        <div className="border-t border-border px-3 py-4 space-y-1">
          <button
            onClick={toggle}
            className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
            {theme === 'dark' ? 'Light Mode' : 'Dark Mode'}
          </button>
          <button
            onClick={handleLogout}
            className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <LogOut size={18} />
            Logout
          </button>
        </div>
      </aside>
    </>
  );
}
