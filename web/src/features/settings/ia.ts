/**
 * Settings pages over the real `settings` domain.
 *
 * The settings IA (nine pages in five groups) — **display metadata**, living in
 * `features/` rather than `api/` (task-26): the gateway stores flat
 * `key → string` rows and knows nothing about language or grouping.
 *
 * Row names come from `internal/contracts` via the generated `SETTING_KEYS`;
 * every label/help/option here is a **catalogue key**, never a literal, so this
 * file names no language at all.
 */
import type { SettingsRows } from '@/api/gateway';
import { SETTING_KEYS as K } from '@/design/settings-keys';
import type { MessageKey } from '@/i18n';

export type SettingsFieldKind = 'text' | 'secret' | 'textarea' | 'number' | 'radio' | 'switch';

export interface SettingsField {
  /** The real row key (`agent.permission`, …). */
  key: string;
  labelKey: MessageKey;
  kind: SettingsFieldKind;
  options?: Array<{ value: string; labelKey: MessageKey }>;
  helpKey?: MessageKey;
  /**
   * An example value (`CmdOrCtrl+Shift+Space`, `http://127.0.0.1:7890`). This is
   * **data**, not copy: every locale shows the same format hint.
   */
  example?: string;
  /** Applied when the row is absent, so a switch does not read as "off" blindly. */
  defaultValue?: string;
}

export interface SettingsSection {
  titleKey?: MessageKey;
  fields: SettingsField[];
  /**
   * A section rendered by a dedicated component instead of generic fields.
   * The model catalog needs list + add/edit/delete against one JSON row; a textarea
   * of raw JSON was the placeholder this replaces.
   */
  component?: 'modelCatalog';
}

export interface SettingsPage {
  key: string;
  labelKey: MessageKey;
  order: number;
  group: { key: string; labelKey: MessageKey; rank: number };
  sections: SettingsSection[];
}

const secret = (key: string, labelKey: MessageKey, helpKey?: MessageKey): SettingsField => ({
  key,
  labelKey,
  kind: 'secret',
  helpKey,
});

const text = (
  key: string,
  labelKey: MessageKey,
  helpKey?: MessageKey,
  example?: string,
): SettingsField => ({
  key,
  labelKey,
  kind: 'text',
  ...(helpKey ? { helpKey } : {}),
  ...(example ? { example } : {}),
});

const json = (key: string, labelKey: MessageKey, helpKey?: MessageKey): SettingsField => ({
  key,
  labelKey,
  kind: 'textarea',
  ...(helpKey ? { helpKey } : {}),
});

const bool = (key: string, labelKey: MessageKey, helpKey?: MessageKey): SettingsField => ({
  key,
  labelKey,
  kind: 'switch',
  defaultValue: 'false',
  ...(helpKey ? { helpKey } : {}),
});

const radio = (
  key: string,
  labelKey: MessageKey,
  options: Array<{ value: string; labelKey: MessageKey }>,
  helpKey?: MessageKey,
): SettingsField => ({ key, labelKey, kind: 'radio', options, ...(helpKey ? { helpKey } : {}) });

const COMMON: SettingsPage['group'] = { key: 'common', labelKey: 'settings.ia.group.common', rank: 12 };
const INTERFACE: SettingsPage['group'] = { key: 'interface', labelKey: 'settings.ia.group.interface', rank: 30 };
const CONNECTIONS: SettingsPage['group'] = { key: 'connections', labelKey: 'settings.ia.group.connections', rank: 50 };
const DATA: SettingsPage['group'] = { key: 'data', labelKey: 'settings.ia.group.data', rank: 70 };
const ABOUT: SettingsPage['group'] = { key: 'about', labelKey: 'settings.ia.about', rank: 90 };

/**
 * The nine pages (R13–R21). A page lists only rows the backend actually stores;
 * a page whose rows are not wired yet says so instead of inventing controls.
 */
export const SETTINGS_PAGES: SettingsPage[] = [
  {
    key: 'model',
    labelKey: 'settings.ia.model',
    order: 10,
    group: COMMON,
    sections: [
      {
        titleKey: 'settings.ia.modelCatalog',
        component: 'modelCatalog',
        // Declared for the IA honesty test and for search; the component renders it.
        fields: [json(K.modelCatalogModels, 'settings.ia.modelCatalog', 'settings.ia.help.modelCatalog')],
      },
      {
        titleKey: 'settings.ia.agentPresets',
        fields: [json(K.agentPresets, 'settings.ia.agentPresets', 'settings.ia.help.presets')],
      },
      {
        // The `chat.*` rows the backend stores: without them the model page
        // silently ignored the settings the agent actually reads.
        titleKey: 'settings.ia.modelProvider',
        fields: [
          text(K.chatModel, 'settings.ia.model'),
          text(K.chatBaseURL, 'settings.ia.baseUrl', 'settings.ia.help.baseUrl'),
          secret(K.chatAPIKey, 'settings.ia.apiKey'),
          { key: K.chatContextBudget, labelKey: 'settings.ia.contextBudget', kind: 'number' },
          { key: K.chatMode, labelKey: 'settings.ia.chatMode', kind: 'text' },
          json(K.chatSystemPrompt, 'settings.ia.systemPrompt', 'settings.ia.help.systemPrompt'),
        ],
      },
    ],
  },
  {
    key: 'behavior',
    labelKey: 'settings.ia.behavior',
    order: 20,
    group: COMMON,
    sections: [
      {
        titleKey: 'settings.ia.permissions',
        fields: [
          radio(
            K.agentPermission,
            'settings.ia.permissionMode',
            [
              { value: 'read-only', labelKey: 'settings.ia.permissionReadOnly' },
              { value: 'workspace', labelKey: 'settings.ia.permissionWorkspace' },
              { value: 'full', labelKey: 'settings.ia.permissionFull' },
            ],
            'settings.ia.help.permission',
          ),
          json(K.sandboxWritableDirs, 'settings.ia.writableDirs', 'settings.ia.help.writableDirs'),
          text(K.agentMode, 'settings.ia.agentMode'),
          text(K.agentsFile, 'settings.ia.agentsFile', 'settings.ia.help.agentsFile'),
        ],
      },
      {
        titleKey: 'settings.ia.limits',
        fields: [
          { key: K.agentSubagentMaxConcurrent, labelKey: 'settings.ia.subagentConcurrency', kind: 'number' },
          { key: K.agentTeamMaxConcurrent, labelKey: 'settings.ia.teamConcurrency', kind: 'number' },
          { key: K.agentApprovalTimeoutSeconds, labelKey: 'settings.ia.approvalTimeout', kind: 'number' },
          { key: K.toolkitShellTimeoutSeconds, labelKey: 'settings.ia.shellTimeout', kind: 'number' },
          { key: K.toolkitShellMaxOutputKiB, labelKey: 'settings.ia.shellMaxOutput', kind: 'number' },
          json(K.toolkitDeniedGlobs, 'settings.ia.deniedPatterns', 'settings.ia.help.jsonGlobArray'),
        ],
      },
    ],
  },
  {
    key: 'appearance',
    labelKey: 'settings.ia.appearance',
    order: 10,
    group: INTERFACE,
    sections: [
      {
        // B5: the row the whole client's language follows.
        titleKey: 'settings.ia.language',
        fields: [
          radio(
            K.uiLanguage,
            'settings.ia.language',
            [
              { value: 'zh', labelKey: 'settings.ia.langZh' },
              { value: 'en', labelKey: 'settings.ia.langEn' },
            ],
            'settings.ia.help.language',
          ),
        ],
      },
      {
        titleKey: 'settings.ia.theme',
        fields: [
          radio(
            K.themeMode,
            'settings.ia.theme',
            [
              { value: 'system', labelKey: 'settings.ia.themeSystem' },
              { value: 'light', labelKey: 'settings.ia.themeLight' },
              { value: 'dark', labelKey: 'settings.ia.themeDark' },
            ],
            'settings.ia.help.theme',
          ),
          bool(K.uiThemeDark, 'settings.ia.themeDarkPreference'),
          { key: K.chatFontSize, labelKey: 'settings.ia.fontSize', kind: 'number' },
          radio(
            K.workStepsMode,
            'settings.ia.stepDisplay',
            [
              { value: 'compact', labelKey: 'settings.ia.verbosityBrief' },
              { value: 'standard', labelKey: 'settings.ia.verbosityStandard' },
              { value: 'detailed', labelKey: 'settings.ia.verbosityDetailed' },
              { value: 'verbose', labelKey: 'settings.ia.alwaysExpanded' },
            ],
          ),
          radio(K.workStepsCollapse, 'settings.ia.collapseWhen', [
            { value: 'completion', labelKey: 'settings.ia.afterAnswer' },
            { value: 'next-input', labelKey: 'settings.ia.onNewMessage' },
          ]),
          text(
            K.globalHotkey,
            'settings.ia.hotkeys',
            'settings.ia.help.trayOnly',
            'CmdOrCtrl+Shift+Space',
          ),
          text(K.uiShortcutCommandPalette, 'settings.ia.shortcutPalette', undefined, 'Mod+K'),
          text(K.uiShortcutToggleSidebar, 'settings.ia.shortcutSidebar', undefined, 'Mod+B'),
        ],
      },
    ],
  },
  {
    key: 'notifications',
    labelKey: 'settings.ia.notifications',
    order: 10,
    group: INTERFACE,
    sections: [
      {
        titleKey: 'settings.ia.verbosity',
        fields: [
          bool(K.notifications, 'settings.ia.inAppNotifications'),
          bool(K.systemNotifications, 'settings.ia.systemNotifications'),
          bool(K.autostart, 'settings.ia.launchAtLogin'),
          bool(K.closeToTray, 'settings.ia.minimizeToTray'),
        ],
      },
    ],
  },
  {
    key: 'network',
    labelKey: 'settings.ia.networkAndSearch',
    order: 30,
    group: CONNECTIONS,
    sections: [
      {
        titleKey: 'settings.ia.proxy',
        fields: [
          text(K.netProxy, 'settings.ia.httpProxy', undefined, 'http://127.0.0.1:7890'),
          text(K.netNoProxy, 'settings.ia.noProxyList'),
        ],
      },
      {
        titleKey: 'settings.ia.search',
        fields: [text(K.webSearchEndpoint, 'settings.ia.searchEndpoint'), secret(K.webSearchAPIKey, 'settings.ia.apiKey')],
      },
    ],
  },
  {
    key: 'lsp',
    labelKey: 'settings.ia.lsp',
    order: 50,
    group: CONNECTIONS,
    sections: [
      {
        titleKey: 'settings.ia.server',
        fields: [
          bool(K.lspActive, 'settings.ia.lspActive'),
          json(K.lspServers, 'settings.ia.languageServer', 'settings.ia.help.agentsJson'),
          json(K.lspWorkspaceConfig, 'settings.ia.workspaceConfig', 'settings.ia.help.workspaceOverrides'),
          text(K.lspServer, 'settings.ia.lspServer', 'settings.ia.help.lspServer'),
        ],
      },
    ],
  },
  {
    key: 'archive',
    labelKey: 'settings.ia.archive',
    order: 30,
    group: DATA,
    sections: [
      {
        titleKey: 'settings.ia.sessions',
        fields: [bool(K.sessionsShowArchived, 'settings.ia.showArchived')],
      },
    ],
  },
  {
    key: 'data-privacy',
    labelKey: 'settings.ia.dataAndPrivacy',
    order: 40,
    group: DATA,
    sections: [
      {
        titleKey: 'settings.ia.toolsAndSkills',
        fields: [
          json(K.toolsDisabled, 'settings.ia.disabledTools', 'settings.ia.help.jsonStringArray'),
          json(K.skillsDisabled, 'settings.ia.disabledSkills', 'settings.ia.help.jsonStringArray'),
          json(K.skillsDirs, 'settings.ia.skillCatalog', 'settings.ia.help.jsonStringArray'),
          json(K.mcpServers, 'settings.ia.mcpServers', 'settings.ia.help.mcpJson'),
          json(K.mcpDisabledServers, 'settings.ia.disabledMcp', 'settings.ia.help.jsonStringArray'),
          json(K.mcpDisabledTools, 'settings.ia.disabledMcpTools', 'settings.ia.help.jsonStringArray'),
          text(K.mcpWorkspaceOptIn, 'settings.ia.mcpWorkspaceOptIn', 'settings.ia.help.mcpWorkspaceOptIn'),
        ],
      },
    ],
  },
  {
    key: 'about',
    labelKey: 'settings.ia.about',
    order: 10,
    group: ABOUT,
    sections: [{ titleKey: 'settings.ia.version', fields: [] }],
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