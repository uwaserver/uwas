import { useState, useEffect, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { KeyRound, AlertCircle, ShieldCheck, User } from 'lucide-react';
import { setToken, setTOTPCode, fetchStats, loginUser } from '@/lib/api';

type Tab = 'apikey' | 'user';
type Step = 'login' | 'totp';

export default function Login() {
  const [tab, setTab] = useState<Tab>('apikey');
  const [step, setStep] = useState<Step>('login');

  // API key auth
  const [key, setKey] = useState('');

  // Username/password auth
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  // TOTP
  const [totp, setTotp] = useState('');

  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  useEffect(() => {
    if (searchParams.get('2fa') === 'required') {
      setStep('totp');
      setError('2FA verification required');
    }
  }, [searchParams]);

  const handleAPIKeySubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!key.trim()) return;
    setLoading(true);
    setError('');
    setToken(key.trim());
    try {
      await fetchStats();
      navigate('/');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '';
      if (msg === '2FA required') {
        setStep('totp');
        setError('');
      } else {
        setError('Invalid API key or server unavailable');
        setToken('');
      }
    } finally {
      setLoading(false);
    }
  };

  const handleUserSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password) return;
    setLoading(true);
    setError('');
    try {
      const result = await loginUser(username.trim(), password);
      setToken(result.token);
      navigate('/');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Login failed';
      setError(msg);
    } finally {
      setLoading(false);
    }
  };

  const handleTOTPSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (totp.length !== 6) return;
    setLoading(true);
    setError('');
    setTOTPCode(totp.trim());
    try {
      await fetchStats();
      navigate('/');
    } catch {
      setError('Invalid 2FA code');
      setTOTPCode('');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm">
        {/* Logo */}
        <div className="mb-8 text-center">
          <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-blue-600 text-xl font-bold sm:text-2xl text-white shadow-lg shadow-blue-600/25">
            U
          </div>
          <h1 className="text-xl font-bold sm:text-2xl text-foreground">UWAS Dashboard</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {step === 'totp' ? 'Enter your 2FA code' : 'Sign in to manage your server'}
          </p>
        </div>

        {/* Form */}
        <div className="rounded-xl border border-border bg-card p-6 shadow-xl">
          {error && (
            <div className="mb-4 flex items-center gap-2 rounded-md bg-red-500/10 px-3 py-2 text-sm text-red-400">
              <AlertCircle size={16} />
              {error}
            </div>
          )}

          {step === 'totp' ? (
            <form onSubmit={handleTOTPSubmit}>
              <label htmlFor="totpCode" className="mb-1.5 block text-sm font-medium text-card-foreground">
                Authenticator Code
              </label>
              <div className="relative">
                <ShieldCheck size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
                <input id="totpCode" type="text" inputMode="numeric" maxLength={6}
                  value={totp} onChange={e => setTotp(e.target.value.replace(/\D/g, ''))}
                  placeholder="000000" autoFocus
                  className="w-full rounded-md border border-border bg-background py-2.5 pr-3 pl-10 text-center text-lg font-mono tracking-widest text-foreground outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500" />
              </div>
              <p className="mt-2 text-xs text-muted-foreground">Enter the 6-digit code from your authenticator app</p>
              <button type="submit" disabled={loading || totp.length !== 6}
                className="mt-4 w-full rounded-md bg-blue-600 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50">
                {loading ? 'Verifying...' : 'Verify'}
              </button>
              <button type="button" onClick={() => { setStep('login'); setError(''); setTotp(''); }}
                className="mt-2 w-full text-sm text-muted-foreground hover:text-card-foreground">
                Back
              </button>
            </form>
          ) : (
            <>
              {/* Tabs */}
              <div className="mb-4 flex rounded-lg bg-muted p-1">
                <button onClick={() => { setTab('apikey'); setError(''); }}
                  className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition ${tab === 'apikey' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
                  API Key
                </button>
                <button onClick={() => { setTab('user'); setError(''); }}
                  className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition ${tab === 'user' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>
                  Username
                </button>
              </div>

              {tab === 'apikey' ? (
                <form onSubmit={handleAPIKeySubmit}>
                  <label htmlFor="apiKey" className="mb-1.5 block text-sm font-medium text-card-foreground">API Key</label>
                  <div className="relative">
                    <KeyRound size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
                    <input id="apiKey" type="password" value={key} onChange={e => setKey(e.target.value)}
                      placeholder="Enter API Key" autoFocus
                      className="w-full rounded-md border border-border bg-background py-2.5 pr-3 pl-10 text-sm text-foreground outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500" />
                  </div>
                  <button type="submit" disabled={loading || !key.trim()}
                    className="mt-4 w-full rounded-md bg-blue-600 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50">
                    {loading ? 'Verifying...' : 'Sign In'}
                  </button>
                </form>
              ) : (
                <form onSubmit={handleUserSubmit}>
                  <div className="space-y-3">
                    <div>
                      <label htmlFor="username" className="mb-1.5 block text-sm font-medium text-card-foreground">Username</label>
                      <div className="relative">
                        <User size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
                        <input id="username" type="text" value={username} onChange={e => setUsername(e.target.value)}
                          placeholder="admin" autoFocus
                          className="w-full rounded-md border border-border bg-background py-2.5 pr-3 pl-10 text-sm text-foreground outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500" />
                      </div>
                    </div>
                    <div>
                      <label htmlFor="password" className="mb-1.5 block text-sm font-medium text-card-foreground">Password</label>
                      <div className="relative">
                        <KeyRound size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
                        <input id="password" type="password" value={password} onChange={e => setPassword(e.target.value)}
                          placeholder="Password"
                          className="w-full rounded-md border border-border bg-background py-2.5 pr-3 pl-10 text-sm text-foreground outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500" />
                      </div>
                    </div>
                  </div>
                  <button type="submit" disabled={loading || !username.trim() || !password}
                    className="mt-4 w-full rounded-md bg-blue-600 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50">
                    {loading ? 'Signing in...' : 'Sign In'}
                  </button>
                </form>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
