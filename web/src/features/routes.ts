import { matchPath } from 'react-router';
import type { MessageKey } from '@/i18n';

/**
 * The static route table (docs/WEB-CLIENT.md §3.2 C2): `screenId → component`
 * is a compile-time mapping resolved by `features/app-routes.tsx`.
 *
 * Every route below is built and rendered: R1 现在, R2 收件箱, R3 会话,
 * R4 轨迹, R5 记忆, R7 子会话, R8–R10 文件树/预览、R11 检查器 guide、
 * R13–R21 九个设置页、R22 自动化、R23 能力、R24 洞察、R26 团队（看板 + 成员）, plus `*` → NotFound.
 * **A route is either built or not registered** (WEB-CLIENT §1.2): a registered
 * placeholder makes the user believe a feature exists, and the honest mechanism
 * for a missing backend half is capability gating — the entry is not rendered at
 * all (see `TAB_CAPABILITY`). That is why there is no `deferral` field: it only
 * documented a state that no longer exists.
 */
export interface RouteMeta {
  /** URL pattern, also the react-router path. */
  path: string;
  /** R-number in docs/WEB-CLIENT.md §1.2. */
  ref: string;
  /** Copy key for the top band (`i18n/zh.ts`), never a literal. */
  titleKey: MessageKey;
}

export const ROUTES: RouteMeta[] = [
  { path: '/', ref: 'R1', titleKey: 'now.title' },
  { path: '/inbox', ref: 'R2', titleKey: 'inbox.title' },
  // R2b: the gateway's inbound work queue (`inbox.*`, TRANSPORT §5.1.13). A
  // separate route from R2 because they answer different questions and their
  // rows are different entities.
  { path: '/inbox/incoming', ref: 'R2b', titleKey: 'inbox.work.title' },
  { path: '/c/:sessionId', ref: 'R3', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/trajectory', ref: 'R4', titleKey: 'thread.tabs.trajectory' },
  { path: '/c/:sessionId/memory', ref: 'R5', titleKey: 'thread.tabs.memory' },
  { path: '/c/:sessionId/context', ref: 'R6', titleKey: 'thread.tabs.context' },
  { path: '/c/:sessionId/child/:childId', ref: 'R7', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/files', ref: 'R8', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/files/*', ref: 'R9/R10', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/guide', ref: 'R11', titleKey: 'thread.untitled' },
  { path: '/settings/:page', ref: 'R13–R21', titleKey: 'settings.title' },
  // R27a: channel configuration (`credentials.*`, `webhook.source*`,
  // `notify.route.*`). Its own entry, not one of the R13–R21 settings pages:
  // the static path outranks `:page` by length in `routeForPath`, so
  // `/settings/channels` resolves here while `/settings/model` still resolves
  // to R13–R21.
  { path: '/settings/channels', ref: 'R27a', titleKey: 'channels.title' },
  { path: '/automation', ref: 'R22', titleKey: 'shell.automation' },
  { path: '/capabilities', ref: 'R23', titleKey: 'shell.capabilities' },
  { path: '/insights', ref: 'R24', titleKey: 'shell.insights' },
  // R26 stays the roster (spawn / message / interrupt): that is what the
  // acceptance list pins at `/team`. The task board is its own surface at
  // `/team/board` because "what is the team doing" is a different question from
  // "who is on the team".
  { path: '/team', ref: 'R26', titleKey: 'team.title' },
  { path: '/team/board', ref: 'R26a', titleKey: 'team.board.title' },
  // R3b: one task on the work board (`tasks.*`, TRANSPORT §5.1.8/§5.1.13).
  // The band shows the section's own name; the task's subject is the screen's
  // `<h1>` (a row title in the band would be stale the moment it is renamed).
  { path: '/t/:taskId', ref: 'R3b', titleKey: 'task.detail.title' },
];

/**
 * The nearest matching entry for a pathname.
 *
 * An exact route always beats a splat, and only then does the longer path win.
 * Both rules are load-bearing: react-router lets a splat match the empty rest,
 * so `/c/:id/files/*` also matches `/c/:id/files` — sorting on length alone
 * handed the file-tree URL to the preview entry (found by `routes.test.ts`).
 */
export function routeForPath(pathname: string): RouteMeta | undefined {
  const rank = (route: RouteMeta) => (route.path.includes('*') ? 0 : 1_000) + route.path.length;
  return ROUTES.filter((route) => matchPath({ path: route.path, end: true }, pathname)).sort(
    (a, b) => rank(b) - rank(a),
  )[0];
}

/** Thread sub-tabs. `context` is missing on purpose: R6 only appears when the
 * backend registers KeyContext, and the mock does not (docs/WEB-CLIENT.md §8.2 B6). */
export const THREAD_TABS = [
  { key: 'conversation', labelKey: 'thread.tabs.conversation' as const, suffix: '' },
  { key: 'trajectory', labelKey: 'thread.tabs.trajectory' as const, suffix: '/trajectory' },
  { key: 'memory', labelKey: 'thread.tabs.memory' as const, suffix: '/memory' },
  { key: 'context', labelKey: 'thread.tabs.context' as const, suffix: '/context' },
  // The file inspector had no visible way in: the route rendered, but reaching
  // it needed a keyboard command nobody could discover (round-2 P0-5). It is a
  // tab, like the other thread surfaces.
  { key: 'files', labelKey: 'thread.tabs.files' as const, suffix: '/files' },
] as const;

/**
 * Tabs whose entry is conditional on a gateway capability (§4.2). The context
 * tab appears only when `initialize` advertised `context` — "missing key ⇒
 * hidden surface", not "always render and fail".
 */
export const TAB_CAPABILITY: Record<string, string> = { context: 'context' };