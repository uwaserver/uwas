import { useCallback, useMemo, useState, type FormEvent, type ReactNode } from 'react';
import { AlertTriangle, Info, X } from 'lucide-react';
import { ConfirmContext, type BaseDialogOptions, type PromptOptions } from './useConfirm';

type DialogVariant = 'danger' | 'warning' | 'info';

type DialogState =
  | { kind: 'confirm'; options: BaseDialogOptions; resolve: (value: boolean) => void }
  | { kind: 'prompt'; options: PromptOptions; value: string; resolve: (value: string | null) => void };

const variantClasses: Record<DialogVariant, { icon: string; button: string }> = {
  danger: {
    icon: 'bg-red-500/10 text-red-400',
    button: 'bg-red-600 text-white hover:bg-red-700',
  },
  warning: {
    icon: 'bg-amber-500/10 text-amber-400',
    button: 'bg-amber-600 text-white hover:bg-amber-700',
  },
  info: {
    icon: 'bg-blue-500/10 text-blue-400',
    button: 'bg-blue-600 text-white hover:bg-blue-700',
  },
};

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [dialog, setDialog] = useState<DialogState | null>(null);

  const confirmAction = useCallback((options: BaseDialogOptions) => new Promise<boolean>((resolve) => {
    setDialog({ kind: 'confirm', options, resolve });
  }), []);

  const promptText = useCallback((options: PromptOptions) => new Promise<string | null>((resolve) => {
    setDialog({ kind: 'prompt', options, value: options.defaultValue ?? '', resolve });
  }), []);

  const value = useMemo(() => ({ confirmAction, promptText }), [confirmAction, promptText]);

  const closeConfirm = (result: boolean) => {
    if (dialog?.kind === 'confirm') dialog.resolve(result);
    setDialog(null);
  };

  const closePrompt = (result: string | null) => {
    if (dialog?.kind === 'prompt') dialog.resolve(result);
    setDialog(null);
  };

  const submitPrompt = (e: FormEvent) => {
    e.preventDefault();
    if (dialog?.kind === 'prompt') closePrompt(dialog.value);
  };

  const updatePromptValue = (value: string) => {
    setDialog(current => current?.kind === 'prompt' ? { ...current, value } : current);
  };

  const opts = dialog?.options;
  const variant = opts?.variant ?? 'warning';
  const styles = variantClasses[variant];

  return (
    <ConfirmContext.Provider value={value}>
      {children}
      {dialog && opts && (
        <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" onClick={() => dialog.kind === 'confirm' ? closeConfirm(false) : closePrompt(null)}>
          <div className="w-full max-w-md rounded-xl border border-border bg-card p-5 shadow-2xl" onClick={e => e.stopPropagation()}>
            <div className="mb-4 flex items-start justify-between gap-3">
              <div className="flex items-center gap-3">
                <div className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${styles.icon}`}>
                  {variant === 'info' ? <Info size={17} /> : <AlertTriangle size={17} />}
                </div>
                <div>
                  <h2 className="text-sm font-semibold text-foreground">{opts.title}</h2>
                  {opts.message && <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{opts.message}</div>}
                </div>
              </div>
              <button
                type="button"
                onClick={() => dialog.kind === 'confirm' ? closeConfirm(false) : closePrompt(null)}
                className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                aria-label="Close"
              >
                <X size={16} />
              </button>
            </div>

            {dialog.kind === 'prompt' ? (
              <form onSubmit={submitPrompt} className="space-y-4">
                {dialog.options.multiline ? (
                  <textarea
                    value={dialog.value}
                    onChange={e => updatePromptValue(e.target.value)}
                    placeholder={dialog.options.placeholder}
                    autoFocus
                    className="h-40 w-full resize-none rounded-md border border-border bg-background px-3 py-2.5 font-mono text-sm text-foreground outline-none focus:border-blue-500"
                  />
                ) : (
                  <input
                    value={dialog.value}
                    onChange={e => updatePromptValue(e.target.value)}
                    placeholder={dialog.options.placeholder}
                    autoFocus
                    className="w-full rounded-md border border-border bg-background px-3 py-2.5 text-sm text-foreground outline-none focus:border-blue-500"
                  />
                )}
                <div className="flex gap-2">
                  <button type="button" onClick={() => closePrompt(null)} className="flex-1 rounded-md border border-border bg-card py-2 text-sm text-card-foreground hover:bg-accent">
                    {opts.cancelLabel ?? 'Cancel'}
                  </button>
                  <button type="submit" className={`flex-1 rounded-md py-2 text-sm font-medium ${styles.button}`}>
                    {opts.confirmLabel ?? 'Continue'}
                  </button>
                </div>
              </form>
            ) : (
              <div className="flex gap-2">
                <button type="button" onClick={() => closeConfirm(false)} className="flex-1 rounded-md border border-border bg-card py-2 text-sm text-card-foreground hover:bg-accent">
                  {opts.cancelLabel ?? 'Cancel'}
                </button>
                <button type="button" onClick={() => closeConfirm(true)} className={`flex-1 rounded-md py-2 text-sm font-medium ${styles.button}`}>
                  {opts.confirmLabel ?? 'Confirm'}
                </button>
              </div>
            )}
          </div>
        </div>
      )}
    </ConfirmContext.Provider>
  );
}
