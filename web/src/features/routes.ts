import { matchPath } from 'react-router';

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
  title: string;
  /**
   * Why a route is unreachable. Only R6 has one today — every other route has
   * a real screen, and claiming otherwise in this field is how the comment
   * drifted out of date once already.
   */
  deferral?: string;
}

export const ROUTES: RouteMeta[] = [
  { path: '/', ref: 'R1', title: '现在' },
  { path: '/inbox', ref: 'R2', title: '收件箱' },
  { path: '/c/:sessionId', ref: 'R3', title: '会话' },
  { path: '/c/:sessionId/trajectory', ref: 'R4', title: '轨迹' },
  { path: '/c/:sessionId/memory', ref: 'R5', title: '记忆' },
  { path: '/c/:sessionId/context', ref: 'R6', title: '上下文', deferral: '后端无 KeyContext 前该 Tab 不出现' },
  { path: '/c/:sessionId/child/:childId', ref: 'R7', title: '子会话' },
  { path: '/c/:sessionId/files', ref: 'R8', title: '文件' },
  { path: '/c/:sessionId/files/*path', ref: 'R9/R10', title: '文件预览' },
  { path: '/c/:sessionId/guide', ref: 'R11', title: '开始' },
  { path: '/settings/:page', ref: 'R13–R21', title: '设置' },
  { path: '/automation', ref: 'R22', title: '自动化' },
  { path: '/capabilities', ref: 'R23', title: '能力' },
  { path: '/insights', ref: 'R24', title: '洞察' },
  { path: '/team', ref: 'R26', title: '团队' },
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
  { key: 'conversation', label: '对话', suffix: '' },
  { key: 'trajectory', label: '轨迹', suffix: '/trajectory' },
  { key: 'memory', label: '记忆', suffix: '/memory' },
] as const;