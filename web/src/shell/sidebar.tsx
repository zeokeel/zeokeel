import { useEffect, useRef, useState } from 'react';
import { App as AntApp, Button, Input } from 'antd';
import { Conversations } from '@ant-design/x';
import {
  archiveSession,
  deleteSession,
  pinSession,
  renameSession,
  subscribeSettings,
  type SettingsRows,
} from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';
import { useSessions } from '@/features/sessions/sessions-context';
import { SidebarNav } from './sidebar-nav';
import { useT } from '@/i18n';

/**
 * The session list. `Conversations` owns the list semantics (selection, item
 * roles, the creation row); this component only supplies data and the
 * navigation side effect, so the client keeps no second list implementation.
 */
/** Open a modal one frame later, after the key event that asked for it. */
function openNextFrame(run: () => void): void {
  window.requestAnimationFrame(run);
}

export function Sidebar({
  activeKey,
  activeSessionId,
  collapsed = false,
  onSelect,
  onCreate,
  onCleared,
}: {
  activeKey?: string;
  /** The open conversation; kept on screen as a dot when collapsed. */
  activeSessionId?: string;
  collapsed?: boolean;
  onSelect: (sessionId: string) => void;
  onCreate: () => void;
  /** The open conversation was deleted or archived: leave the thread view. */
  onCleared?: () => void;
}) {
  const { sessions, loading, error, reload } = useSessions();
  const t = useT();
  const { modal } = AntApp.useApp();
  // The trigger that opened the menu, so focus can come back to it: antd closes
  // the dropdown on Esc/selection and leaves the document focused on BODY
  // (round-3 session audit F2b).
  const lastTrigger = useRef<HTMLButtonElement | null>(null);
  // Also remember *which* session the menu belonged to: pinning re-orders the
  // list, React rebuilds the rows, and the remembered node becomes detached —
  // `focus()` on it fails silently and the keyboard user lands on BODY
  // (round-5 session audit F2f).
  const lastSessionId = useRef<string | null>(null);

  // Bounded retry: a pin/unpin re-orders the rows on the *next* delta, so a
  // single rAF fires while the old node is still being replaced. Retrying for
  // ~1.5s is enough for the reorder to land and costs nothing when it does not.
  const focusSessionTrigger = (sessionId: string | null) => {
    const deadline = Date.now() + 2000;
    const attempt = () => {
      // Give up only if the user moved focus somewhere of their own accord: a
      // re-order lands as a later delta, so "focus once" always loses.
      const active = document.activeElement;
      const inMenu = active instanceof HTMLElement && active.closest('.ant-dropdown') !== null;
      if (!inMenu && active instanceof HTMLElement && active !== document.body && active.isConnected) {
        const isOurTrigger = active.getAttribute('data-testid') === 'session-menu';
        if (!isOurTrigger) return;
        if (sessionId && active.getAttribute('data-session-id') === sessionId) return;
      }
      // Same-element selector: `data-session-id` is ON the button, so a
      // descendant selector matched nothing and every restore fell through to
      // "the first row" (round-6 session audit).
      const selector = sessionId
        ? `[data-testid="session-menu"][data-session-id="${CSS.escape(sessionId)}"]`
        : null;
      const target =
        (selector ? document.querySelector<HTMLElement>(selector) : null) ??
        document.querySelector<HTMLElement>('[data-testid="session-menu"]');
      target?.focus();
      if (Date.now() < deadline) window.setTimeout(attempt, 80);
    };
    window.requestAnimationFrame(attempt);
  };

  const promptRename = (current: string) =>
    new Promise<string | null>((resolve) => {
      let draft = current;
      // Controlled: the field must show what will be stored. A plain maxLength
      // counts UTF-16 units (100 emoji looked like the limit) and an onChange
      // that only truncated the draft let the input display 201 characters while
      // saving 200 (round-3 session audit, note).
      const RenameField = ({ onDraft }: { onDraft: (value: string) => void }) => {
        const [text, setText] = useState(current);
        return (
          <Input
            value={text}
            autoFocus
            onChange={(event) => {
              const next = [...event.target.value].slice(0, 200).join('');
              setText(next);
              onDraft(next);
            }}
          />
        );
      };
      openNextFrame(() =>
      modal.confirm({
        title: t('shell.sessions.renameTitle'),
        content: <RenameField onDraft={(value) => { draft = value; }} />,
        okText: t('shell.sessions.save'),
        cancelText: t('shell.sessions.cancel'),
        onOk: () => resolve(draft),
        onCancel: () => resolve(null),
        afterClose: () => focusSessionTrigger(lastSessionId.current),
      }),
      );
    });

  const confirmDelete = (title: string) =>
    new Promise<boolean>((resolve) => {
      openNextFrame(() =>
      modal.confirm({
        title: t('shell.sessions.deleteTitle', { title }),
        content: t('shell.sessions.deleteBody'),
        okText: t('shell.sessions.delete'),
        cancelText: t('shell.sessions.cancel'),
        okButtonProps: { danger: true },
        // 取消 is the focused button: the keystroke that chose 删除 in the menu
        // used to land on the confirm's first button, so Enter "confirmed"
        // itself (round-4 session audit F2d).
        autoFocusButton: 'cancel',
        onOk: () => resolve(true),
        onCancel: () => resolve(false),
        afterClose: () => focusSessionTrigger(lastSessionId.current),
      }),
      );
    });

  // `ui.sessions.showArchived` is a real switch: the list only carried archived
  // conversations when the row said so, and the row said nothing at all before
  // (round-2 S13 — a setting that changed no pixel).
  const [rows, setRows] = useState<SettingsRows>({});
  useEffect(() => subscribeSettings(setRows), []);
  const showArchived = String(rows[SETTING_KEYS.sessionsShowArchived] ?? 'false') === 'true';
  const visible = showArchived ? sessions : sessions.filter((session) => !session.archived);

  // Pinned first, then the gateway's own order. The pin flag lived only in a
  // settings row before: the menu said 取消置顶 and nothing on screen changed
  // (round-3 session audit F1).
  const ordered = [...visible].sort((a, b) => Number(b.pinned === true) - Number(a.pinned === true));
  const items = ordered.map((session) => ({
    key: session.id,
    // The label is the title and nothing else: the pin used to be spliced into
    // it, so renaming pre-filled "标题 · 置顶" and the marker ended up inside the
    // stored title (round-3 session audit F1b).
    label: session.title || t('shell.sessions.untitled'),
    ...(session.pinned
      ? {
          icon: (
            <span className="zk-session-pin" title={t('shell.sessions.pinnedMark')} aria-label={t('shell.sessions.pinnedMark')}>
              <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
                <path d="M4 1.5h4l-.6 3 2 2v1H2.6v-1l2-2z" fill="currentColor" />
                <path d="M6 7.5v3" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
              </svg>
            </span>
          ),
        }
      : {}),
  }));

  // A re-order (pin/unpin) rebuilds the rows *after* the action's promise
  // resolves — the delta arrives later — so focusing once right after the
  // action is too early: the node is replaced and focus falls to BODY. Whenever
  // the list changes and focus is nowhere, put it back on the trigger that owns
  // the menu (round-5 session audit F2f).
  useEffect(() => {
    if (!lastSessionId.current) return;
    // Only when the list change ate the focus (a row moved or vanished).
    if (document.activeElement instanceof HTMLElement && document.activeElement !== document.body) return;
    focusSessionTrigger(lastSessionId.current);
  }, [visible]);

  // Esc closes the dropdown natively; without this the focus dies with it.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      const open = document.querySelector('.ant-dropdown:not(.ant-dropdown-hidden)');
      if (!open) return;
      window.requestAnimationFrame(() => lastTrigger.current?.focus());
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, []);

  const handleMenu = async (key: string, sessionId: string, title: string) => {
    try {
      if (key === 'rename') {
        const next = await promptRename(title);
        if (next === null || next.trim() === '' || next === title) return;
        await renameSession(sessionId, next.trim());
        return;
      }
      if (key === 'pin') {
        const session = sessions.find((item) => item.id === sessionId);
        await pinSession(sessionId, session?.pinned !== true);
        return;
      }
      if (key === 'archive') {
        const session = sessions.find((item) => item.id === sessionId);
        const archived = session?.archived !== true;
        await archiveSession(sessionId, archived);
        if (archived && sessionId === activeSessionId) onCleared?.();
        return;
      }
      if (key === 'delete') {
        const confirmed = await confirmDelete(title || t('shell.sessions.untitled'));
        if (!confirmed) return;
        await deleteSession(sessionId);
        try {
          localStorage.removeItem(`zk.draft.${sessionId}`);
        } catch {
          // Storage may be unavailable; the conversation is gone either way.
        }
        if (sessionId === activeSessionId) onCleared?.();
      }
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : String(cause);
      void modal.error({ title: t('shell.sessions.actionFailed'), content: message });
    }
  };

  return (
    <div className="zk-sidebar" data-collapsed={collapsed ? 'true' : 'false'}>
      <div className="zk-sidebar__header">
        {collapsed ? null : <span className="zk-sidebar__brand">{t('app.name')}</span>}
        {/* Text, not filled: 新建会话 is on every screen and must not hold an
            Accent fill (rule 10: ≤2 fills / ≤1 primary per screen). */}
        <Button
          size="small"
          type="text"
          onClick={onCreate}
          aria-label={t('shell.newSession')}
          data-testid="new-session"
          title={collapsed ? t('shell.newSession') : undefined}
        >
          {collapsed ? '+' : t('shell.newSession')}
        </Button>
      </div>
      <div className="zk-sidebar__nav">
        <SidebarNav collapsed={collapsed} />
      </div>
      <div className="zk-sidebar__list">
        {collapsed ? (
          // Collapsed: the session list becomes a rail of dots; the open one is
          // marked so "which conversation am I in" survives the fold (B16).
          <div className="zk-sidebar__rail" aria-label={t('shell.sessions.rail')}>
            {items.map((item) => (
              <button
                key={item.key}
                type="button"
                className="zk-sidebar__rail-dot"
                data-active={item.key === activeSessionId ? 'true' : 'false'}
                aria-label={item.label}
                title={item.label}
                onClick={() => onSelect(item.key)}
              />
            ))}
          </div>
        ) : (
          <Conversations
            items={items}
            // The row menu is a list-level prop in @ant-design/x: a per-item
            // `menu` is ignored, which is why the first attempt showed nothing
            // when hovered.
            menu={(conversation) => {
              const session = sessions.find((item) => item.id === conversation.key);
              return {
                items: [
                  { key: 'rename', label: t('shell.sessions.rename') },
                  {
                    key: 'pin',
                    label: session?.pinned ? t('shell.sessions.unpin') : t('shell.sessions.pin'),
                  },
                  {
                    key: 'archive',
                    // An archived row must offer the way back; the old menu only
                    // ever sent `archived: true` (round-3 session audit F3).
                    label: session?.archived
                      ? t('shell.sessions.unarchive')
                      : t('shell.sessions.archive'),
                  },
                  { type: 'divider' as const },
                  { key: 'delete', label: t('shell.sessions.delete'), danger: true },
                ],
                // A real button, not antd's hover-only icon: the row itself is
                // not focusable (`li` has no tabindex) and the default trigger is
                // `tabindex="-1"` + `opacity: 0` until hover, so keyboard and
                // touch users could not open the menu at all (F2/F6).
                trigger: (
                  <button
                    type="button"
                    className="zk-conv-menu-btn"
                    aria-label={t('shell.sessions.menu')}
                    data-testid="session-menu"
                    data-session-id={String(conversation.key)}
                    aria-haspopup="menu"
                    onClick={(event) => {
                      // Which row opened the menu: a ref callback would run for
                      // every row and leave the last one, so focus came back to
                      // the wrong conversation (round-4 session audit F2c).
                      lastTrigger.current = event.currentTarget;
                      lastSessionId.current = String(conversation.key);
                    }}
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowDown' && event.key !== 'Enter' && event.key !== ' ') {
                        return;
                      }
                      lastTrigger.current = event.currentTarget;
                      lastSessionId.current = String(conversation.key);
                      if (event.key === 'ArrowDown') {
                        // The Dropdown opens on click only; ArrowDown has to be
                        // the same gesture or the key does nothing at all (F2a).
                        event.preventDefault();
                        event.currentTarget.click();
                      }
                      // Enter/Space already open it through the click handler;
                      // ArrowDown needs the same treatment. Either way focus has
                      // to land on the first item, or the keyboard user is stuck
                      // on the trigger with a menu they cannot reach (F2′).
                      window.setTimeout(() => {
                        const first = document.activeElement?.closest('.ant-dropdown')
                          ? null
                          : document.querySelector<HTMLElement>(
                              '.ant-dropdown:not(.ant-dropdown-hidden) .ant-dropdown-menu-item',
                            );
                        first?.focus();
                      }, 80);
                    }}
                  >
                    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
                      <circle cx="8" cy="3.5" r="1.3" fill="currentColor" />
                      <circle cx="8" cy="8" r="1.3" fill="currentColor" />
                      <circle cx="8" cy="12.5" r="1.3" fill="currentColor" />
                    </svg>
                  </button>
                ),
                onClick: (info) => {
                  info.domEvent.stopPropagation();
                  const target = sessions.find((item) => item.id === conversation.key);
                  lastSessionId.current = String(conversation.key);
                  void handleMenu(
                    String(info.key),
                    String(conversation.key),
                    target?.title ?? '',
                  ).finally(() => focusSessionTrigger(lastSessionId.current));
                },
              };
            }}
            activeKey={activeKey}
            onActiveChange={(key) => onSelect(String(key))}
          />
        )}
        {loading ? <div className="zk-sidebar__empty">{t('shell.sessions.loading')}</div> : null}
        {error ? (
          <div className="zk-sidebar__empty">
            <span>{t('shell.sessions.failed', { error })}</span>
            <div>
              <Button size="small" type="link" onClick={() => void reload()}>
                {t('shell.sessions.retry')}
              </Button>
            </div>
          </div>
        ) : null}
        {!loading && !error && sessions.length === 0 ? (
          <div className="zk-sidebar__empty">{t('shell.sessions.empty')}</div>
        ) : null}
      </div>
    </div>
  );
}