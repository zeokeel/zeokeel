import { Badge, Menu } from 'antd';
import { useLocation, useNavigate } from 'react-router';
import { useApprovals } from '@/features/approvals/approvals-context';
import { ZkIcon } from '@/design/icons';
import { useT } from '@/i18n';

/**
 * The sidebar's navigation (S-02 `sidebar.nav`): Inbox, capabilities,
 * automation, insights, team, settings.
 *
 * These entries are always visible, even at zero, because a feature the user
 * cannot find is a feature that does not exist (AGENTS.md §3.17). Only the
 * approvals count is a badge — it is the actionable one.
 */
export function SidebarNav({ collapsed = false }: { collapsed?: boolean } = {}) {
  const navigate = useNavigate();
  const location = useLocation();
  const { approvals } = useApprovals();
  const t = useT();

  const selected =
    ['/inbox', '/capabilities', '/automation', '/insights', '/team'].find((path) =>
      location.pathname.startsWith(path),
    ) ?? (location.pathname.startsWith('/settings') ? '/settings/model' : '');

  return (
    <Menu
      mode="inline"
      // Collapsed (Mod+B): icons only, with the label kept as a tooltip so the
      // rail is still readable by hover and by screen readers.
      inlineCollapsed={collapsed}
      selectedKeys={selected ? [selected] : []}
      onClick={({ key }) => navigate(key)}
      items={[
        {
          key: '/inbox',
          icon: <ZkIcon name="message" />,
          label: (
            <span className="zk-nav-label">
               {t('shell.inbox')}
              {approvals.length > 0 ? (
                <Badge
                  count={approvals.length}
                  size="small"
                  title={t('shell.approvalsBadge', { count: approvals.length })}
                  aria-label={t('shell.approvalsBadge', { count: approvals.length })}
                />
              ) : null}
            </span>
          ),
        },
        { key: '/capabilities', icon: <ZkIcon name="plugin" />, label: t('shell.capabilities') },
        { key: '/automation', icon: <ZkIcon name="clock" />, label: t('shell.automation') },
        { key: '/insights', icon: <ZkIcon name="activity" />, label: t('shell.insights') },
        // C6: always visible, and never with a `0` badge — zero state is an
        // empty surface inside, not a count on the entry.
        { key: '/team', icon: <ZkIcon name="users" />, label: t('shell.team') },
        { key: '/settings/model', icon: <ZkIcon name="sliders" />, label: t('shell.settings') },
      ]}
    />
  );
}