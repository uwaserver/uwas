import { AlertTriangle } from 'lucide-react';
import type { FeatureStatus } from '@/lib/api';

interface Props {
  feature: string;
  status: FeatureStatus | undefined | null;
  /** Optional human-readable feature label shown in the heading. */
  label?: string;
}

/**
 * Renders nothing if the feature is enabled (or status is still loading).
 * Otherwise shows an amber banner explaining the feature is not initialized,
 * so the empty list below isn't deceptive.
 */
export default function FeatureBanner({ feature, status, label }: Props) {
  if (!status || status.enabled) return null;

  const title = label ?? feature.replace(/_/g, ' ');

  return (
    <div className="rounded-lg border border-amber-300 bg-amber-50 p-4 dark:border-amber-700 dark:bg-amber-950/30">
      <div className="flex items-start gap-3">
        <AlertTriangle className="h-5 w-5 text-amber-600 dark:text-amber-400 mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <h3 className="font-semibold text-amber-900 dark:text-amber-200 capitalize">
            {title} is not enabled on this server
          </h3>
          {status.reason && (
            <p className="text-sm text-amber-800 dark:text-amber-300/90 mt-1">
              {status.reason}
            </p>
          )}
          <p className="text-xs text-amber-700 dark:text-amber-400 mt-2">
            Empty list below ≠ no data — the feature is just not running.
          </p>
        </div>
      </div>
    </div>
  );
}
