import { useState, useCallback } from 'react';
import { Stethoscope, Wrench, CheckCircle, AlertTriangle, XCircle, Sparkles } from 'lucide-react';

const BASE = import.meta.env.DEV ? 'http://127.0.0.1:9443' : '';

interface Check {
  name: string;
  status: 'ok' | 'warn' | 'fail' | 'fixed';
  message: string;
  fix?: string;
  how_to?: string;
}

interface DoctorReport {
  checks: Check[];
  summary: string;
}

async function fetchDoctor(fix: boolean): Promise<DoctorReport> {
  const token = localStorage.getItem('uwas_token') || '';
  const url = fix ? `${BASE}/api/v1/doctor/fix` : `${BASE}/api/v1/doctor`;
  const res = await fetch(url, {
    method: fix ? 'POST' : 'GET',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

const statusIcon = (s: string) => {
  switch (s) {
    case 'ok': return <CheckCircle size={16} className="text-emerald-400" />;
    case 'warn': return <AlertTriangle size={16} className="text-amber-400" />;
    case 'fail': return <XCircle size={16} className="text-red-400" />;
    case 'fixed': return <Sparkles size={16} className="text-blue-400" />;
    default: return null;
  }
};

const statusBg = (s: string) => {
  switch (s) {
    case 'ok': return 'border-emerald-500/20 bg-emerald-500/5';
    case 'warn': return 'border-amber-500/20 bg-amber-500/5';
    case 'fail': return 'border-red-500/20 bg-red-500/5';
    case 'fixed': return 'border-blue-500/20 bg-blue-500/5';
    default: return 'border-[#334155]';
  }
};

export default function Doctor() {
  const [report, setReport] = useState<DoctorReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [fixing, setFixing] = useState(false);

  const runDiagnose = useCallback(async () => {
    setLoading(true);
    try { setReport(await fetchDoctor(false)); } catch {}
    finally { setLoading(false); }
  }, []);

  const runFix = useCallback(async () => {
    setFixing(true);
    try { setReport(await fetchDoctor(true)); } catch {}
    finally { setFixing(false); }
  }, []);

  const counts = report?.checks.reduce((acc, c) => {
    acc[c.status] = (acc[c.status] || 0) + 1;
    return acc;
  }, {} as Record<string, number>) ?? {};

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">System Doctor</h1>
          <p className="text-sm text-slate-400">Diagnose issues and auto-fix problems</p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={runDiagnose} disabled={loading}
            className="flex items-center gap-2 rounded-md border border-[#334155] bg-[#1e293b] px-4 py-2 text-sm font-medium text-slate-200 hover:bg-[#334155] disabled:opacity-50">
            <Stethoscope size={14} className={loading ? 'animate-pulse' : ''} />
            {loading ? 'Scanning...' : 'Diagnose'}
          </button>
          <button onClick={runFix} disabled={fixing}
            className="flex items-center gap-2 rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50">
            <Wrench size={14} className={fixing ? 'animate-spin' : ''} />
            {fixing ? 'Fixing...' : 'Auto-Fix'}
          </button>
        </div>
      </div>

      {!report && !loading && (
        <div className="rounded-lg border border-[#334155] bg-[#1e293b] p-12 text-center">
          <Stethoscope size={48} className="mx-auto mb-4 text-slate-600" />
          <p className="text-slate-400">Click <strong>Diagnose</strong> to scan your system for issues</p>
          <p className="mt-1 text-xs text-slate-500">Or <strong>Auto-Fix</strong> to scan and repair automatically</p>
        </div>
      )}

      {report && (
        <>
          {/* Summary bar */}
          <div className="flex items-center gap-4 rounded-lg border border-[#334155] bg-[#1e293b] px-5 py-3">
            <span className="text-sm font-medium text-slate-300">{report.summary}</span>
            <div className="ml-auto flex gap-3 text-xs">
              {counts.ok > 0 && <span className="text-emerald-400">{counts.ok} OK</span>}
              {counts.warn > 0 && <span className="text-amber-400">{counts.warn} Warnings</span>}
              {counts.fail > 0 && <span className="text-red-400">{counts.fail} Failures</span>}
              {counts.fixed > 0 && <span className="text-blue-400">{counts.fixed} Fixed</span>}
            </div>
          </div>

          {/* Checks */}
          <div className="space-y-2">
            {report.checks.map((c, i) => (
              <div key={i} className={`rounded-lg border p-4 ${statusBg(c.status)}`}>
                <div className="flex items-center gap-3">
                  {statusIcon(c.status)}
                  <span className="font-medium text-slate-200">{c.name}</span>
                  <span className="ml-auto text-sm text-slate-400">{c.message}</span>
                </div>
                {c.how_to && (
                  <div className="mt-2 rounded bg-[#0f172a] px-3 py-2 text-xs font-mono text-slate-400">
                    {c.how_to}
                  </div>
                )}
                {c.fix && (
                  <div className="mt-2 flex items-center gap-2 text-xs text-blue-400">
                    <Sparkles size={12} />
                    Auto-fixed: {c.fix}
                  </div>
                )}
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
