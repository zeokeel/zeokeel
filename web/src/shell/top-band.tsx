import { Tabs } from 'antd';
import { matchPath, useLocation, useNavigate } from 'react-router';
import { THREAD_TABS, routeForPath } from '@/features/routes';
import { useSessions } from '@/features/sessions/sessions-context';
import { useT } from '@/i18n';
import { ConnectionBadge } from './connection-badge';

/**
 * The top band: screen title plus, on a thread, its sub-tabs. Tabs live in the
 * URL (R4/R5), never in component memory, so a refresh keeps the surface
 * (docs/WEB-CLIENT.md §1.2).
 */
export function TopBand() {
  const location = useLocation();
  const navigate = useNavigate();
  const { sessions } = useSessions();
  const t = useT();

  const threadMatch = matchPath('/c/:sessionId/*', location.pathname);
  const sessionId = threadMatch?.params.sessionId;
  const route = routeForPath(location.pathname);

  const session = sessionId ? sessions.find((item) => item.id === sessionId) : undefined;
  const title = sessionId
    ? session?.title || t('thread.untitled')
    : route
      ? t(route.titleKey)
      : t('app.name');

  const activeTab = THREAD_TABS.find(
    (tab) => tab.suffix !== '' && location.pathname.endsWith(tab.suffix),
  );
  const activeKey = activeTab?.key ?? 'conversation';

  return (
    <header className="zk-topband" role="banner" aria-label={t('thread.banner')}>
      <div className="zk-topband__row">
        <h1 className="zk-topband__title">{title}</h1>
        <ConnectionBadge />
      </div>
      {sessionId ? (
        <Tabs
          size="small"
          activeKey={activeKey}
          items={THREAD_TABS.map((tab) => ({ key: tab.key, label: t(tab.labelKey) }))}
          tabBarStyle={{ marginBottom: 0 }}
          onChange={(key) => {
            const target = THREAD_TABS.find((tab) => tab.key === key);
            if (!target) return;
            navigate(`/c/${sessionId}${target.suffix}`);
          }}
        />
      ) : null}
    </header>
  );
}