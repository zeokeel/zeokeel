import { useEffect, useState } from 'react';
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

  const promptRename = (current: string) =>
    new Promise<string | null>((resolve) => {
      let draft = current;
      modal.confirm({
        title: t('shell.sessions.renameTitle'),
        content: (
          <Input
            defaultValue={current}
            autoFocus
            // maxLength counts UTF-16 units, so 100 emoji looked like the limit;
            // the gateway counts runes (round-3 session audit, note).
            onChange={(event) => {
              draft = [...event.target.value].slice(0, 200).join('');
            }}
          />
        ),
        okText: t('shell.sessions.save'),
        cancelText: t('settings.revert'),
        onOk: () => resolve(draft),
        onCancel: () => resolve(null),
      });
    });

  const confirmDelete = (title: string) =>
    new Promise<boolean>((resolve) => {
      modal.confirm({
        title: t('shell.sessions.deleteTitle', { title }),
        content: t('shell.sessions.deleteBody'),
        okText: t('shell.sessions.delete'),
        cancelText: t('settings.revert'),
        okButtonProps: { danger: true },
        onOk: () => resolve(true),
        onCancel: () => resolve(false),
      });
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
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowDown' && event.key !== 'Enter' && event.key !== ' ') {
                        return;
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
                  void handleMenu(
                    String(info.key),
                    String(conversation.key),
                    target?.title ?? '',
                  );
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