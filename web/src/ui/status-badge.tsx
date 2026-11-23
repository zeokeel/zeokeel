import type { ReactNode } from 'react';

/**
 * One status vocabulary for the whole client: semantic tint + a required text
 * label, never colour alone (AGENTS.md §3.10). `running` uses the accent, as a
 * running turn is not a warning; warning is reserved for needs-action.
 */
export type StatusTone = 'running' | 'success' | 'error' | 'neutral' | 'warning';

const TONE_LABEL: Record<StatusTone, string> = {
  running: 'thread.status.running',
  success: 'thread.status.done',
  error: 'thread.status.failed',
  neutral: 'ui.status.cancelled',
  warning: 'ui.status.needsAction',
};

export function StatusBadge({
  tone,
  label,
  children,
}: {
  tone: StatusTone;
  label?: string;
  children?: ReactNode;
}) {
  return (
    <span className={`zk-badge zk-badge--${tone}`}>
      <span className="zk-badge__dot" aria-hidden="true" />
      <span className="zk-badge__label">{label ?? TONE_LABEL[tone]}</span>
      {children}
    </span>
  );
}