import { createContext, useContext, type ReactNode } from 'react';

type DialogVariant = 'danger' | 'warning' | 'info';

export interface BaseDialogOptions {
  title: string;
  message?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: DialogVariant;
}

export interface PromptOptions extends BaseDialogOptions {
  placeholder?: string;
  defaultValue?: string;
  multiline?: boolean;
}

export interface ConfirmContextValue {
  confirmAction: (options: BaseDialogOptions) => Promise<boolean>;
  promptText: (options: PromptOptions) => Promise<string | null>;
}

export const ConfirmContext = createContext<ConfirmContextValue | null>(null);

export function useConfirm() {
  const ctx = useContext(ConfirmContext);
  if (!ctx) {
    throw new Error('useConfirm must be used inside ConfirmProvider');
  }
  return ctx;
}
