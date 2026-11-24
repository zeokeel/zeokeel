import { useEffect, useState } from 'react';
import { Button } from 'antd';
import { Conversations } from '@ant-design/x';
import { subscribeSettings, type SettingsRows } from '@/api/gateway';
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
}: {
  activeKey?: string;
  /** The open conversation; kept on screen as a dot when collapsed. */
  activeSessionId?: string;
  collapsed?: boolean;
  onSelect: (sessionId: string) => void;
  onCreate: () => void;
}) {
  const { sessions, loading, error, reload } = useSessions();
  const t = useT();

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