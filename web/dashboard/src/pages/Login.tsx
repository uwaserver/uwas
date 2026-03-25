import { useState, useEffect, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { KeyRound, AlertCircle, ShieldCheck } from 'lucide-react';
import { setToken, setTOTPCode, fetchStats } from '@/lib/api';

export default function Login() {
  const [key, setKey] = useState('');
  const [totp, setTotp] = useState('');
  const [step, setStep] = useState<'key' | 'totp'>('key');
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

  const handleKeySubmit = async (e: FormEvent) => {
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
            {step === 'key' ? 'Enter your API key to continue' : 'Enter your 2FA code'}
          </p>
        </div>

        {/* Form */}
        <form
          onSubmit={step === 'key' ? handleKeySubmit : handleTOTPSubmit}
          className="rounded-xl border border-border bg-card p-6 shadow-xl"
        >
          {error && (
            <div className="mb-4 flex items-center gap-2 rounded-md bg-red-500/10 px-3 py-2 text-sm text-red-400">
              <AlertCircle size={16} />
              {error}
            </div>
          )}

          {step === 'key' ? (
            <>
              <label htmlFor="apiKey" className="mb-1.5 block text-sm font-medium text-card-foreground">
                API Key
              </label>
              <div className="relative">
                <KeyRound size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
                <input
                  id="apiKey"
                  type="password"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                  placeholder="Enter API Key"
                  autoFocus
                  className="w-full rounded-md border border-border bg-background py-2.5 pr-3 pl-10 text-sm text-foreground placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </>
          ) : (
            <>
              <label htmlFor="totpCode" className="mb-1.5 block text-sm font-medium text-card-foreground">
                Authenticator Code
              </label>
              <div className="relative">
                <ShieldCheck size={16} className="absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
                <input
                  id="totpCode"
                  type="text"
                  inputMode="numeric"
                  maxLength={6}
                  value={totp}
                  onChange={(e) => setTotp(e.target.value.replace(/\D/g, ''))}
                  placeholder="000000"
                  autoFocus
                  className="w-full rounded-md border border-border bg-background py-2.5 pr-3 pl-10 text-center text-lg font-mono tracking-widest text-foreground placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <p className="mt-2 text-xs text-muted-foreground">
                Enter the 6-digit code from your authenticator app
              </p>
            </>
          )}

          <button
            type="submit"
            disabled={loading || (step === 'key' ? !key.trim() : totp.length !== 6)}
            className="mt-4 w-full rounded-md bg-blue-600 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {loading ? 'Verifying...' : step === 'key' ? 'Sign In' : 'Verify'}
          </button>

          {step === 'totp' && (
            <button
              type="button"
              onClick={() => { setStep('key'); setError(''); setTotp(''); }}
              className="mt-2 w-full text-sm text-muted-foreground hover:text-card-foreground"
            >
              Back to API key
            </button>
          )}
        </form>
      </div>
    </div>
  );
}
