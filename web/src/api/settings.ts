/**
 * Settings pages over the real `settings` domain.
 *
 * The IA (nine pages in five groups) is the product's own and lives here — the
 * gateway only stores flat `key → string` rows (`settings.snapshot {rows}`,
 * command `settings.set {key,value}`). Row names come from
 * `internal/contracts/settingskeys.go` and friends; nothing here invents a
 * setting the backend does not have.
 */
import type { SettingsRows } from './gateway';
import { SETTING_KEYS as K } from '@/design/settings-keys';

export type SettingsFieldKind = 'text' | 'textarea' | 'number' | 'radio' | 'switch';

export interface SettingsField {
  /** The real row key (`agent.permission`, …). */
  key: string;
  label: string;
  kind: SettingsFieldKind;
  options?: Array<{ value: string; label: string }>;
  help?: string;
  placeholder?: string;
  /** Applied when the row is absent, so a switch does not read as "off" blindly. */
  defaultValue?: string;
}

export interface SettingsSection {
  title?: string;
  fields: SettingsField[];
}

export interface SettingsPage {
  key: string;
  label: string;
  order: number;
  group: { key: string; label: string; rank: number };
  sections: SettingsSection[];
}

const text = (key: string, label: string, help?: string, placeholder?: string): SettingsField => ({
  key,
  label,
  kind: 'text',
  ...(help ? { help } : {}),
  ...(placeholder ? { placeholder } : {}),
});

const json = (key: string, label: string, help?: string): SettingsField => ({
  key,
  label,
  kind: 'textarea',
  ...(help ? { help } : {}),
});

const bool = (key: string, label: string, help?: string): SettingsField => ({
  key,
  label,
  kind: 'switch',
  defaultValue: 'false',
  ...(help ? { help } : {}),
});

const radio = (
  key: string,
  label: string,
  options: Array<{ value: string; label: string }>,
  help?: string,
): SettingsField => ({ key, label, kind: 'radio', options, ...(help ? { help } : {}) });

const COMMON = { key: 'common', label: '常用', rank: 12 };
const INTERFACE = { key: 'interface', label: '界面与通知', rank: 30 };
const CONNECTIONS = { key: 'connections', label: '连接', rank: 50 };
const DATA = { key: 'data', label: '数据', rank: 70 };
const ABOUT = { key: 'about', label: '关于', rank: 90 };

/**
 * The nine pages (R13–R21). A page lists only rows the backend actually stores;
 * a page whose rows are not wired yet says so instead of inventing controls.
 */
export const SETTINGS_PAGES: SettingsPage[] = [
  {
    key: 'model',
    label: '模型',
    order: 10,
    group: COMMON,
    sections: [
      {
        title: '模型目录',
        fields: [
          json('modelcatalog.models', '模型目录', 'JSON：模型 id 与显示名。留空使用内置目录。'),
          json('presets.agents', 'Agent 预设', 'JSON：预设列表。留空使用内置预设。'),
        ],
      },
    ],
  },
  {
    key: 'behavior',
    label: '行为与限额',
    order: 20,
    group: COMMON,
    sections: [
      {
        title: '权限',
        fields: [
          radio(
            K.agentPermission,
            '权限模式',
            [
              { value: 'read-only', label: '只能看' },
              { value: 'workspace', label: '可以改文件' },
              { value: 'full', label: '什么都能做' },
            ],
            '审批与操作系统沙箱都跟随它。',
          ),
          json('sandbox.writableDirs', '沙箱可写目录', 'JSON 字符串数组；留空表示只允许工作文件夹。'),
        ],
      },
      {
        title: '限额',
        fields: [
          { key: K.agentSubagentMaxConcurrent, label: '子代理并发上限', kind: 'number' },
          { key: K.agentTeamMaxConcurrent, label: '团队并发上限', kind: 'number' },
          { key: K.agentApprovalTimeoutSeconds, label: '审批超时（秒）', kind: 'number' },
          { key: K.toolkitShellTimeoutSeconds, label: '命令超时（秒）', kind: 'number' },
          { key: K.toolkitShellMaxOutputKiB, label: '命令输出上限（KiB）', kind: 'number' },
          json(K.toolkitDeniedGlobs, '禁止访问的模式', 'JSON 字符串数组（glob）。'),
        ],
      },
    ],
  },
  {
    key: 'appearance',
    label: '外观',
    order: 10,
    group: INTERFACE,
    sections: [
      {
        title: '主题',
        fields: [
          radio(
            K.themeMode,
            '主题',
            [
              { value: 'system', label: '跟随系统' },
              { value: 'light', label: '浅色' },
              { value: 'dark', label: '深色' },
            ],
            '浏览器端随系统外观；写入后由网关保存。',
          ),
          { key: K.chatFontSize, label: '对话字号', kind: 'number' },
          radio(
            K.workStepsMode,
            '工作步骤展示',
            [
              { value: 'compact', label: '简洁' },
              { value: 'standard', label: '标准' },
              { value: 'detailed', label: '详细' },
              { value: 'verbose', label: '完全展开' },
            ],
          ),
          radio(K.workStepsCollapse, '收起时机', [
            { value: 'completion', label: '回答结束后' },
            { value: 'next-input', label: '下次有新消息' },
          ]),
          text(
            K.globalHotkey,
            '全局热键',
            '托盘壳使用；浏览器端不生效。',
            'CmdOrCtrl+Shift+Space',
          ),
        ],
      },
    ],
  },
  {
    key: 'notifications',
    label: '通知',
    order: 10,
    group: INTERFACE,
    sections: [
      {
        title: '分级',
        fields: [
          bool(K.notifications, '应用内通知'),
          bool(K.systemNotifications, '系统通知'),
          bool(K.autostart, '开机自启（托盘壳）'),
          bool(K.closeToTray, '关闭时最小化到托盘（托盘壳）'),
        ],
      },
    ],
  },
  {
    key: 'network',
    label: '网络与搜索',
    order: 30,
    group: CONNECTIONS,
    sections: [
      {
        title: '代理',
        fields: [
          text(K.netProxy, 'HTTP 代理', undefined, 'http://127.0.0.1:7890'),
          text(K.netNoProxy, '直连列表'),
        ],
      },
      {
        title: '搜索',
        fields: [text(K.webSearchEndpoint, '搜索端点'), text(K.webSearchAPIKey, 'API Key')],
      },
    ],
  },
  {
    key: 'lsp',
    label: 'LSP',
    order: 50,
    group: CONNECTIONS,
    sections: [
      {
        title: '服务器',
        fields: [
          json(K.lspServers, '语言服务器', 'JSON 数组：{name, command}。'),
          json('lsp.workspace_config', '工作区配置', 'JSON：按工作区覆盖。'),
        ],
      },
    ],
  },
  {
    key: 'archive',
    label: '归档',
    order: 30,
    group: DATA,
    sections: [
      {
        title: '会话',
        fields: [bool('ui.sessions.showArchived', '在侧栏显示已归档会话')],
      },
    ],
  },
  {
    key: 'data-privacy',
    label: '数据与隐私',
    order: 40,
    group: DATA,
    sections: [
      {
        title: '工具与技能',
        fields: [
          json('tools.disabled', '禁用的工具', 'JSON 字符串数组。'),
          json('skills.disabled', '禁用的技能', 'JSON 字符串数组。'),
          json('skills.dirs', '技能目录', 'JSON 字符串数组。'),
          json('mcp.servers', 'MCP 服务器', 'JSON 数组：{name, transport, command|url}。'),
          json('mcp.disabledServers', '禁用的 MCP 服务器', 'JSON 字符串数组。'),
        ],
      },
    ],
  },
  {
    key: 'about',
    label: '关于',
    order: 10,
    group: ABOUT,
    sections: [{ title: '版本', fields: [] }],
  },
];

export function orderedPages(): SettingsPage[] {
  return [...SETTINGS_PAGES].sort((a, b) => {
    if (a.group.rank !== b.group.rank) return a.group.rank - b.group.rank;
    return a.order - b.order;
  });
}

export function findPage(key: string): SettingsPage | undefined {
  return SETTINGS_PAGES.find((page) => page.key === key);
}

export function defaultPageKey(): string {
  return orderedPages()[0]?.key ?? 'model';
}

/** Effective value of one field, applying the documented default. */
export function fieldValue(field: SettingsField, rows: SettingsRows): string {
  const stored = rows[field.key];
  if (stored !== undefined && stored !== null) return String(stored);
  return field.defaultValue ?? '';
}

/** Draft map for one page (row key → string value). */
export function pageValues(page: SettingsPage, rows: SettingsRows): Record<string, string> {
  const values: Record<string, string> = {};
  for (const section of page.sections) {
    for (const field of section.fields) values[field.key] = fieldValue(field, rows);
  }
  return values;
}

/** Which rows changed relative to the gateway's snapshot. */
export function changedRows(
  page: SettingsPage,
  values: Record<string, string>,
  rows: SettingsRows,
): Array<{ key: string; value: string }> {
  const changed: Array<{ key: string; value: string }> = [];
  for (const section of page.sections) {
    for (const field of section.fields) {
      const next = values[field.key] ?? '';
      if (next !== fieldValue(field, rows)) changed.push({ key: field.key, value: next });
    }
  }
  return changed;
}

/** Rows that exist on the gateway but are not on any page yet. */
export function unmappedRows(rows: SettingsRows): string[] {
  const mapped = new Set(
    SETTINGS_PAGES.flatMap((page) =>
      page.sections.flatMap((section) => section.fields.map((field) => field.key)),
    ),
  );
  return Object.keys(rows)
    .filter((key) => !mapped.has(key))
    .sort();
}