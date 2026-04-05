import { useState, useEffect } from 'react';
import { Cpu, HardDrive, MemoryStick, Activity } from 'lucide-react';
import { fetchSystem, type SystemInfo } from '@/lib/api';

export default function SystemStatsBar() {
  const [sys, setSys] = useState<SystemInfo | null>(null);

  useEffect(() => {
    const load = () => fetchSystem().then(setSys).catch(() => {});
    load();
    const id = setInterval(load, 2000);
    return () => clearInterval(id);
  }, []);

  if (!sys) return null;

  const ramPct = sys.ram_total_bytes && sys.ram_available_bytes
    ? Math.round(((sys.ram_total_bytes - sys.ram_available_bytes) / sys.ram_total_bytes) * 100)
    : null;

  const diskPct = sys.disk_total_bytes && sys.disk_free_bytes
    ? Math.round(((sys.disk_total_bytes - sys.disk_free_bytes) / sys.disk_total_bytes) * 100)
    : null;

  return (
    <div className="fixed top-0 left-0 right-0 z-50 flex items-center gap-4 border-b border-border bg-background/95 backdrop-blur px-4 py-1.5 text-xs lg:left-60">
      {/* CPU */}
      <div className="flex items-center gap-1.5 text-muted-foreground" title={`Load: ${sys.load_1m || '?'} (1m), ${sys.load_5m || '?'} (5m)`}>
        <Cpu size={12} />
        <span>{sys.cpus} CPU{/*</span><span className="font-mono text-foreground">{sys.load_1m || '--'}</span>*/}</span>
        {sys.load_1m && (
          <span className="font-mono text-foreground">{sys.load_1m}</span>
        )}
      </div>

      <div className="h-3 w-px bg-border" />

      {/* RAM */}
      <div className="flex items-center gap-1.5 text-muted-foreground" title="RAM usage">
        <MemoryStick size={12} />
        <span>RAM</span>
        <span className="font-mono text-foreground">
          {sys.ram_total_human || '--'}
          {ramPct !== null && (
            <span className={ramPct > 80 ? 'text-red-400' : ramPct > 60 ? 'text-amber-400' : 'text-emerald-400'}>
              {' '}{ramPct}%
            </span>
          )}
        </span>
      </div>

      <div className="h-3 w-px bg-border" />

      {/* Disk */}
      <div className="flex items-center gap-1.5 text-muted-foreground" title="Disk usage">
        <HardDrive size={12} />
        <span>Disk</span>
        <span className="font-mono text-foreground">
          {sys.disk_used_human || sys.disk_total_human || '--'}
          {diskPct !== null && (
            <span className={diskPct > 80 ? 'text-red-400' : diskPct > 60 ? 'text-amber-400' : 'text-emerald-400'}>
              {' '}{diskPct}%
            </span>
          )}
        </span>
      </div>

      <div className="h-3 w-px bg-border" />

      {/* Uptime */}
      <div className="flex items-center gap-1.5 text-muted-foreground" title="Uptime">
        <Activity size={12} />
        <span className="font-mono text-foreground">{sys.uptime || '--'}</span>
      </div>

      {/* Version - right aligned */}
      <div className="ml-auto text-muted-foreground">
        <span className="font-mono">{sys.version || ''}</span>
      </div>
    </div>
  );
}
