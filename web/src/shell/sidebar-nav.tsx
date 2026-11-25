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

  // Longest first: `/inbox/incoming` also starts with `/inbox`, and
  // `/team/members` also starts with `/team`; a nested route must keep its own
  // entry selected.
  const selected =
    [
      '/team/board',
      '/inbox/incoming',
      '/inbox',
      '/capabilities',
      '/automation',
      '/insights',
      '/team',
      // R27a: the channel surface is its own entry, so it must not inherit the
      // settings item's selection (`/settings/model`).
      '/settings/channels',
      // R27b: the run-status surface has its own entry too, so it cannot
      // inherit the settings item's selection either.
      '/settings/health',
      // R27c: the shared note board. Its own entry for the same reason: it must
      // not leave the generic settings item selected.
      '/settings/memory',
      // R27d: the bot roster. Its own entry too — a bot is not a setting.
      '/settings/bots',
      // R27e: the roster of people. Its own entry for the same reason: a policy
      // table about who may act is not a client preference.
      '/settings/users',
      // R28: the skill-drafts queue, also its own entry.
      '/skills/drafts',
    ].find((path) => location.pathname.startsWith(path)) ??
    (location.pathname.startsWith('/settings') ? '/settings/model' : '');

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
        {
          // R2b: the inbound work queue. No count badge — it is a request/
          // response surface, so the entry cannot promise a number it has not
          // read, and a zero badge would be a number worth nothing (C6).
          // There is no `inbox` icon in internal/icons/svg yet, so the neutral
          // box (`data`) stands in until one is generated.
          key: '/inbox/incoming',
          icon: <ZkIcon name="data" />,
          label: t('inbox.work.title'),
        },
        { key: '/capabilities', icon: <ZkIcon name="plugin" />, label: t('shell.capabilities') },
        { key: '/automation', icon: <ZkIcon name="clock" />, label: t('shell.automation') },
        { key: '/insights', icon: <ZkIcon name="activity" />, label: t('shell.insights') },
        // C6: always visible, and never with a `0` badge — zero state is an
        // empty surface inside, not a count on the entry.
        { key: '/team', icon: <ZkIcon name="users" />, label: t('shell.team') },
        // R26b: the roster behind the board. Same icon family (`users`) because
        // it is the same people; the board answers "what are they doing", this
        // one "who is on the team" (spawn/message/interrupt live here).
        { key: '/team/board', icon: <ZkIcon name="data" />, label: t('team.board.nav') },
        // R28: the skill drafts a finished task proposes. `sparkles` is the
        // "something was proposed for you" glyph; `file` is the shared note
        // board, `plugin` the capabilities page, and `check` would promise the
        // queue is already resolved.
        {
          key: '/skills/drafts',
          icon: <ZkIcon name="sparkles" />,
          label: t('skills.drafts.title'),
        },
        { key: '/settings/model', icon: <ZkIcon name="sliders" />, label: t('shell.settings') },
        // R27a: channel credentials / webhook sources / notify routes. `send`
        // (the paper plane) is the delivery direction of everything on that
        // page; `data` is already taken by the inbound work queue.
        { key: '/settings/channels', icon: <ZkIcon name="send" />, label: t('channels.title') },
        // R27b: run status (heartbeat + automation browser). `plug` is the
        // "is it connected and alive" glyph; `activity` is already the insights
        // entry and `clock` the automation one.
        { key: '/settings/health', icon: <ZkIcon name="plug" />, label: t('health.title') },
        // R27c: the shared note board. `file` is the "written note" glyph;
        // `data` is the inbound work queue and `pencil` would promise editing
        // the list, which this surface does not offer.
        { key: '/settings/memory', icon: <ZkIcon name="file" />, label: t('memory.title') },
        // R27d: the bot roster. There is no robot glyph in internal/icons/svg
        // yet, so the neutral unit box (`square`) stands in until one is
        // generated — the same precedent as the inbound work queue's `data`.
        // `users` is the roster of people, `sparkles` the proposals queue, and
        // `sliders` the settings entry: none of them means "a persistent岗位".
        { key: '/settings/bots', icon: <ZkIcon name="square" />, label: t('bots.title') },
        // R27e: the roster of people and the role policy. `shield` is the
        // "who may do what" glyph, which is what this screen is about: `users`
        // already means the team roster above, and `sliders` is the settings
        // entry — neither says "permission".
        { key: '/settings/users', icon: <ZkIcon name="shield" />, label: t('users.title') },
      ]}
    />
  );
}