import { useState, useRef, useEffect } from 'react';
import { Lock, X } from 'lucide-react';

interface PinModalProps {
  open: boolean;
  title?: string;
  message?: string;
  onConfirm: (pin: string) => void;
  onCancel: () => void;
}

export default function PinModal({ open, title, message, onConfirm, onCancel }: PinModalProps) {
  const [pin, setPin] = useState('');
  const [error, setError] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  // Close/cancel: reset state and notify parent.
  const handleCancel = () => {
    setPin('');
    setError('');
    onCancel();
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!pin.trim()) {
      setError('Pin code required');
      return;
    }
    const cleanPin = pin.trim();
    setPin('');
    setError('');
    onConfirm(cleanPin);
  };

  // Focus the input when modal opens.
  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus();
  }, [open]);

  // Escape to close + focus trap.
  useEffect(() => {
    if (!open) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        handleCancel();
        return;
      }
      if (e.key === 'Tab' && dialogRef.current) {
        const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
        );
        if (focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [open]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={handleCancel}>
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="pin-modal-title"
        aria-describedby="pin-modal-desc"
        className="w-full max-w-sm rounded-xl border border-border bg-card p-6 shadow-2xl"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-red-500/10">
              <Lock size={16} className="text-red-400" />
            </div>
            <h2 id="pin-modal-title" className="text-sm font-semibold text-foreground">{title || 'Pin Required'}</h2>
          </div>
          <button onClick={handleCancel} className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground">
            <X size={16} />
          </button>
        </div>

        <p id="pin-modal-desc" className="text-xs text-muted-foreground mb-4">
          {message || 'Enter your pin code to confirm this destructive operation.'}
        </p>

        <form onSubmit={handleSubmit}>
          <input
            ref={inputRef}
            type="password"
            value={pin}
            onChange={e => { setPin(e.target.value); setError(''); }}
            placeholder="Enter pin code"
            autoComplete="off"
            autoFocus
            className={`w-full rounded-md border px-3 py-2.5 text-sm text-center font-mono tracking-widest text-foreground outline-none ${
              error ? 'border-red-500 bg-red-500/5' : 'border-border bg-background focus:border-blue-500'
            }`}
          />
          {error && <p className="mt-1.5 text-xs text-red-400">{error}</p>}

          <div className="mt-4 flex gap-2">
            <button type="button" onClick={handleCancel}
              className="flex-1 rounded-md border border-border bg-card py-2 text-sm text-card-foreground hover:bg-accent">
              Cancel
            </button>
            <button type="submit"
              className="flex-1 rounded-md bg-red-600 py-2 text-sm font-medium text-white hover:bg-red-700">
              Confirm
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
