import { App as AntApp, notification } from 'antd';
import { useEffect, useRef } from 'react';
import { notifyAction, subscribeToasts, type ToastEvent } from '@/api/gateway';

/**
 * Toasts (docs/TRANSPORT.md §6.3, acceptance B14).
 *
 * `toast.posted {id, level, text, sessionId?, actionLabel?, actionId?}` is a
 * one-way event — no snapshot, no delta. Two behaviours are the rule, not a
 * preference:
 *
 *  - **4 秒自动消失**: a toast reports "the thing you just did finished".
 *  - **error 粘滞**: an error needs a decision (retry, read the reason), so it
 *    stays until the user closes it. Making it vanish is how failures get
 *    missed.
 *
 * An action button (撤销/查看) sends `notify.action {id, actionId}` — the
 * gateway keeps the closure, the client never runs it locally.
 */
/**
 * How long a toast stays: 4 seconds for a receipt, forever for an error.
 *
 * Exported because "does a failure disappear on its own?" is the whole point of
 * this surface, and it must be assertable without waiting on a real clock.
 */
export function toastDuration(level: string): number {
  return level === 'error' ? 0 : 4;
}

export function Toasts({ children }: { children: React.ReactNode }) {
  const [api, contextHolder] = notification.useNotification();
  // Notifications are keyed by the gateway's id so a repeat cannot stack.
  const opened = useRef(new Set<string>());

  useEffect(
    () =>
      subscribeToasts((toast: ToastEvent) => {
        if (opened.current.has(toast.id)) return;
        opened.current.add(toast.id);

        const key = `toast-${toast.id}`;
        const type =
          toast.level === 'error' ? 'error' : toast.level === 'warning' ? 'warning' : 'info';

        api.open({
          key,
          type,
          message: toast.text,
          // 4 seconds for a receipt; an error waits for the user.
          duration: toastDuration(toast.level),
          // A toast that dismisses itself must free its id too: the set only
          // grew before, one entry per toast for the life of the page
          // (AUDIT-2026-11-24 C).

          // Top-right: bottom-right is where the composer and its send button
          // live, and a toast must never sit on the thing you are typing into.
          placement: 'topRight',
          btn:
            toast.actionLabel && toast.actionId
              ? [
                  <button
                    key="action"
                    type="button"
                    className="zk-toast__action"
                    onClick={() => {
                      void notifyAction(toast.id, String(toast.actionId)).catch(() => undefined);
                      api.destroy(key);
                    }}
                  >
                    {toast.actionLabel}
                  </button>,
                ]
              : undefined,
          onClose: () => opened.current.delete(toast.id),
        });
      }),
    [api],
  );

  return (
    <AntApp component={false}>
      {contextHolder}
      {children}
    </AntApp>
  );
}