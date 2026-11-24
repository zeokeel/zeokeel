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
            maxLength={200}
            autoFocus
            onChange={(event) => {
              draft = event.target.value;
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

  const items = visible.map((session) => ({
    key: session.id,
    label: session.title || t('shell.sessions.untitled'),
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
        await archiveSession(sessionId, true);
        if (sessionId === activeSessionId) onCleared?.();
        return;
      }
      if (key === 'delete') {
        const confirmed = await confirmDelete(title || t('shell.sessions.untitled'));
        if (!confirmed) return;
        await deleteSession(sessionId);
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
                  { key: 'archive', label: t('shell.sessions.archive') },
                  { type: 'divider' as const },
                  { key: 'delete', label: t('shell.sessions.delete'), danger: true },
                ],
                onClick: (info) => {
                  info.domEvent.stopPropagation();
                  void handleMenu(
                    String(info.key),
                    String(conversation.key),
                    String(conversation.label ?? ''),
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