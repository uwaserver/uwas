import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { KeyRound, AlertCircle } from 'lucide-react';
import { setToken, fetchStats } from '@/lib/api';

export default function Login() {
  const [key, setKey] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!key.trim()) return;

    setLoading(true);
    setError('');
    setToken(key.trim());

    try {
      await fetchStats(); // uses protected endpoint to validate the key
      navigate('/');
    } catch {
      setError('Invalid API key or server unavailable');
      setToken('');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#0f172a] px-4">
      <div className="w-full max-w-sm">
        {/* Logo */}
        <div className="mb-8 text-center">
          <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-blue-600 text-2xl font-bold text-white shadow-lg shadow-blue-600/25">
            U
          </div>
          <h1 className="text-2xl font-bold text-slate-100">UWAS Dashboard</h1>
          <p className="mt-1 text-sm text-slate-400">
            Enter your API key to continue
          </p>
        </div>

        {/* Form */}
        <form
          onSubmit={handleSubmit}
          className="rounded-xl border border-[#334155] bg-[#1e293b] p-6 shadow-xl"
        >
          {error && (
            <div className="mb-4 flex items-center gap-2 rounded-md bg-red-500/10 px-3 py-2 text-sm text-red-400">
              <AlertCircle size={16} />
              {error}
            </div>
          )}

          <label
            htmlFor="apiKey"
            className="mb-1.5 block text-sm font-medium text-slate-300"
          >
            API Key
          </label>
          <div className="relative">
            <KeyRound
              size={16}
              className="absolute top-1/2 left-3 -translate-y-1/2 text-slate-500"
            />
            <input
              id="apiKey"
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="Enter API Key"
              autoFocus
              className="w-full rounded-md border border-[#334155] bg-[#0f172a] py-2.5 pr-3 pl-10 text-sm text-slate-200 placeholder-slate-500 outline-none transition focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <button
            type="submit"
            disabled={loading || !key.trim()}
            className="mt-4 w-full rounded-md bg-blue-600 py-2.5 text-sm font-medium text-white transition hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {loading ? 'Authenticating...' : 'Sign In'}
          </button>
        </form>
      </div>
    </div>
  );
}
