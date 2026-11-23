import { matchPath } from 'react-router';

/**
 * The static route table (docs/WEB-CLIENT.md §3.2 C2): `screenId → component`
 * is a compile-time mapping. Only R1 and R3 are built in this milestone; the
 * rest are registered placeholders so navigation, deep links and the top band
 * already speak the real URL space.
 */
export interface RouteMeta {
  /** URL pattern, also the react-router path. */
  path: string;
  /** R-number in docs/WEB-CLIENT.md §1.2. */
  ref: string;
  title: string;
  /** Where the failure to implement is recorded, for the placeholder body. */
  deferral?: string;
}

export const ROUTES: RouteMeta[] = [
  { path: '/', ref: 'R1', title: '现在' },
  { path: '/inbox', ref: 'R2', title: '收件箱', deferral: '审批 / 提问 / 计划 / 产物 / 完成回执汇聚，随 task-5 落地' },
  { path: '/c/:sessionId', ref: 'R3', title: '会话' },
  { path: '/c/:sessionId/trajectory', ref: 'R4', title: '轨迹', deferral: '工具/推理时间线' },
  { path: '/c/:sessionId/memory', ref: 'R5', title: '记忆', deferral: '会话记忆' },
  { path: '/c/:sessionId/context', ref: 'R6', title: '上下文', deferral: '后端无 KeyContext 前该 Tab 不出现' },
  { path: '/c/:sessionId/child/:childId', ref: 'R7', title: '子会话', deferral: '子代理 / 队友子会话' },
  { path: '/c/:sessionId/files', ref: 'R8', title: '文件', deferral: '右检查器工作区树' },
  { path: '/c/:sessionId/files/*path', ref: 'R9/R10', title: '文件预览', deferral: '只读预览与逐文件 diff' },
  { path: '/c/:sessionId/guide', ref: 'R11', title: '开始', deferral: '检查器 guide 落地页' },
  { path: '/settings/:page', ref: 'R13–R21', title: '设置', deferral: '9 个设置页（M1 只要求模型/行为/外观）' },
  { path: '/automation', ref: 'R22', title: '自动化', deferral: '定时任务 CRUD' },
  { path: '/capabilities', ref: 'R23', title: '能力', deferral: '技能 / MCP 连接器' },
  { path: '/insights', ref: 'R24', title: '洞察', deferral: '跨会话用量（只读）' },
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