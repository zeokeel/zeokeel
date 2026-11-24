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
import { parseShortcut } from '@/keyboard/shortcut-combo';

export type SettingsFieldKind =
  | 'text'
  | 'secret'
  | 'textarea'
  | 'number'
  | 'radio'
  | 'switch'
  | 'json';

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
  /**
   * The unit a numeric row is measured in (`tokens`, `px`, `s`). Rendered inside
   * the control: a bare number with no unit is a field the user has to guess
   * the shape of (round-2 S09).
   */
  unit?: string;
  /**
   * The field is displayed but not editable, because another surface owns the
   * row. Editing it here wrote a value the owner never re-read (round-3 conn
   * audit F7: corrupt JSON looked "saved" and then silently cleared the
   * connectors on the next refresh).
   */
  readOnly?: boolean;
  /**
   * A draft-level check the renderer shows under the field. Returns a catalogue
   * key when the value cannot work, null when it can. It does not block saving:
   * the runtime falls back to a default, and the point of the line is to say so
   * instead of letting a typo live silently in the store (round-3 wire audit).
   */
  validate?: (value: string) => MessageKey | null;
}

export interface SettingsSection {
  titleKey?: MessageKey;
  fields: SettingsField[];
  /**
   * A section rendered by a dedicated component instead of generic fields.
   * The model catalog needs list + add/edit/delete against one JSON row; a textarea
   * of raw JSON was the placeholder this replaces.
   */
  component?: 'modelCatalog' | 'updateStatus';
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

const json = (
  key: string,
  labelKey: MessageKey,
  helpKey?: MessageKey,
  readOnly?: boolean,
): SettingsField => ({
  key,
  labelKey,
  ...(readOnly ? { readOnly } : {}),
  // Structured rows are edited as JSON today; the kind lets the field render in
  // the code face at full width, and names the surface a structured editor
  // (key/value rows) will replace (round-2 S07 / FIELD-DESIGN §3).
  kind: 'json',
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
        // Declared for the IA honesty test and for search; the component renders
        // them (the catalogue rows and the single default-model picker).
        fields: [
          json(K.modelCatalogModels, 'settings.ia.modelCatalog', 'settings.ia.help.modelCatalog'),
          text(K.chatModel, 'settings.ia.model', 'settings.ia.help.defaultModel'),
        ],
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
          // `chat.model` has exactly one control: the default picker at the top
          // of the catalogue. A second free-text field for the same row made the
          // outcome depend on which one the user touched last (round-2 S5).
          text(K.chatBaseURL, 'settings.ia.baseUrl', 'settings.ia.help.baseUrl'),
          secret(K.chatAPIKey, 'settings.ia.apiKey'),
          { key: K.chatContextBudget, labelKey: 'settings.ia.contextBudget', kind: 'number', unit: 'tokens' },
          // The gateway only accepts these three (`plan_mode.go`), and an
          // unknown value used to be normalized silently — a free-text field
          // therefore stored a value the UI then displayed as if it were real
          // (round-3 model audit P2).
          // `defaultValue` mirrors what the gateway does when the row is absent
          // (`plan_mode.go` falls back to standard): showing three unselected
          // radios implied "not decided", which is not the state (audit O1).
          // It is display-only — an untouched field still writes nothing.
          {
            ...radio(
              K.chatMode,
              'settings.ia.chatMode',
              [
                { value: 'standard', labelKey: 'settings.ia.mode.standard' },
                { value: 'minimal', labelKey: 'settings.ia.mode.minimal' },
                { value: 'creator', labelKey: 'settings.ia.mode.creator' },
              ],
              'settings.ia.help.chatMode',
            ),
            defaultValue: 'standard',
          },
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
          { key: K.agentApprovalTimeoutSeconds, labelKey: 'settings.ia.approvalTimeout', kind: 'number', unit: 's' },
          { key: K.toolkitShellTimeoutSeconds, labelKey: 'settings.ia.shellTimeout', kind: 'number', unit: 's' },
          { key: K.toolkitShellMaxOutputKiB, labelKey: 'settings.ia.shellMaxOutput', kind: 'number', unit: 'KiB' },
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
          { key: K.chatFontSize, labelKey: 'settings.ia.fontSize', kind: 'number', unit: 'px' },
          // Both rows default to what the renderer falls back to, so the radio
          // group shows the effective value instead of "nothing selected"
          // (round-3 wire audit). Display-only: an untouched row writes nothing.
          {
            ...radio(
              K.workStepsMode,
              'settings.ia.stepDisplay',
              [
                { value: 'compact', labelKey: 'settings.ia.verbosityBrief' },
                { value: 'standard', labelKey: 'settings.ia.verbosityStandard' },
                { value: 'detailed', labelKey: 'settings.ia.verbosityDetailed' },
                { value: 'verbose', labelKey: 'settings.ia.alwaysExpanded' },
              ],
              'settings.ia.help.stepDisplay',
            ),
            defaultValue: 'standard',
          },
          {
            ...radio(
              K.workStepsCollapse,
              'settings.ia.collapseWhen',
              [
                { value: 'completion', labelKey: 'settings.ia.afterAnswer' },
                { value: 'next-input', labelKey: 'settings.ia.onNewMessage' },
              ],
              'settings.ia.help.collapseWhen',
            ),
            defaultValue: 'completion',
          },
          text(
            K.globalHotkey,
            'settings.ia.hotkeys',
            'settings.ia.help.trayOnly',
            'CmdOrCtrl+Shift+Space',
          ),
          {
            ...text(K.uiShortcutCommandPalette, 'settings.ia.shortcutPalette', 'settings.ia.help.shortcutForm', 'Mod+K'),
            validate: shortcutValueCheck,
          },
          {
            ...text(K.uiShortcutToggleSidebar, 'settings.ia.shortcutSidebar', 'settings.ia.help.shortcutForm', 'Mod+B'),
            validate: shortcutValueCheck,
          },
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
          {
            ...bool(K.notifications, 'settings.ia.inAppNotifications'),
            // The toast gate treats an absent row as "on" (`?? 'true'`); a switch
            // that reads absent as "off" contradicted it (round-3 mobile N3).
            defaultValue: 'true',
          },
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
          // The connectors page owns both rows now; showing them read-only keeps
          // the "where is it stored" answer without offering a second writer.
          json(K.mcpServers, 'settings.ia.mcpServers', 'settings.ia.help.mcpJsonOwned', true),
          json(K.mcpDisabledServers, 'settings.ia.disabledMcp', 'settings.ia.help.mcpDisabledOwned', true),
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
    sections: [
      {
        titleKey: 'settings.ia.version',
        fields: [],
        // The version page is the update surface: current version, the release
        // the gateway last saw, its notes, and check/download/install.
        component: 'updateStatus',
      },
    ],
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
/** The two shortcut rows share one rule: a modifier plus one key. */
function shortcutValueCheck(value: string): MessageKey | null {
  if (value.trim() === '') return null;
  return parseShortcut(value) ? null : 'settings.ia.err.shortcut';
}

export function pageValues(page: SettingsPage, rows: SettingsRows): Record<string, string> {
  const values: Record<string, string> = {};
  for (const section of page.sections) {
    // A section rendered by its own component owns its rows: it writes them as
    // the user acts, and a page draft that also carried them would write the
    // snapshot back over the component's newer value (round-3 model audit P0-A:
    // the footer 保存 resurrected deleted models and rolled back renames).
    if (section.component) continue;
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
    if (section.component) continue;
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