import { matchPath } from 'react-router';
import type { MessageKey } from '@/i18n';

/**
 * The static route table (docs/WEB-CLIENT.md §3.2 C2): `screenId → component`
 * is a compile-time mapping resolved by `features/app-routes.tsx`.
 *
 * Every route below is built and rendered: R1 现在, R2 收件箱, R3 会话,
 * R4 轨迹, R5 记忆, R7 子会话, R8–R10 文件树/预览、R11 检查器 guide、
 * R13–R21 九个设置页、R22 自动化、R23 能力、R24 洞察、R25 团队, plus `*` → NotFound.
 * The only route that is deliberately unreachable is R6 上下文: the gateway has
 * no `context.*` method, so `THREAD_TABS` omits it (docs/WEB-CLIENT.md §8.2 B6).
 */
export interface RouteMeta {
  /** URL pattern, also the react-router path. */
  path: string;
  /** R-number in docs/WEB-CLIENT.md §1.2. */
  ref: string;
  /** Copy key for the top band (`i18n/zh.ts`), never a literal. */
  titleKey: MessageKey;
  /**
   * Why a route is unreachable. Only R6 has one today — every other route has
   * a real screen, and claiming otherwise in this field is how the comment
   * drifted out of date once already.
   */
  deferral?: string;
}

export const ROUTES: RouteMeta[] = [
  { path: '/', ref: 'R1', titleKey: 'now.title' },
  { path: '/inbox', ref: 'R2', titleKey: 'inbox.title' },
  { path: '/c/:sessionId', ref: 'R3', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/trajectory', ref: 'R4', titleKey: 'thread.tabs.trajectory' },
  { path: '/c/:sessionId/memory', ref: 'R5', titleKey: 'thread.tabs.memory' },
  { path: '/c/:sessionId/context', ref: 'R6', titleKey: 'thread.tabs.context', deferral: 'thread.contextUnavailable' },
  { path: '/c/:sessionId/child/:childId', ref: 'R7', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/files', ref: 'R8', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/files/*path', ref: 'R9/R10', titleKey: 'thread.untitled' },
  { path: '/c/:sessionId/guide', ref: 'R11', titleKey: 'thread.untitled' },
  { path: '/settings/:page', ref: 'R13–R21', titleKey: 'settings.title' },
  { path: '/automation', ref: 'R22', titleKey: 'shell.automation' },
  { path: '/capabilities', ref: 'R23', titleKey: 'shell.capabilities' },
  { path: '/insights', ref: 'R24', titleKey: 'shell.insights' },
  { path: '/team', ref: 'R26', titleKey: 'shell.team' },
];

/** The nearest matching entry for a pathname (exact match, longest wins). */
export function routeForPath(pathname: string): RouteMeta | undefined {
  return ROUTES.filter((route) => matchPath({ path: route.path, end: true }, pathname)).sort(
    (a, b) => b.path.length - a.path.length,
  )[0];
}

/** Thread sub-tabs. `context` is missing on purpose: R6 only appears when the
 * backend registers KeyContext, and the mock does not (docs/WEB-CLIENT.md §8.2 B6). */
export const THREAD_TABS = [
  { key: 'conversation', labelKey: 'thread.tabs.conversation' as const, suffix: '' },
  { key: 'trajectory', labelKey: 'thread.tabs.trajectory' as const, suffix: '/trajectory' },
  { key: 'memory', labelKey: 'thread.tabs.memory' as const, suffix: '/memory' },
] as const;