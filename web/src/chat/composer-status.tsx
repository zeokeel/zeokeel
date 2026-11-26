import { useEffect, useState } from 'react';
import { Alert, Select, Space, Typography } from 'antd';
import { loadModels, type ModelRow } from '@/api/read-models';
import { subscribeContext, subscribeSettings } from '@/api/gateway';
import type { SettingsRows } from '@/api/gateway';
import type { ContextView } from '@/api/context';
import { useI18n, useT } from '@/i18n';
import { SETTING_KEYS } from '@/design/settings-keys';

/**
 * The composer's small components, as the framework's own slots.
 *
 * antd X `Sender` owns the input box and exposes `prefix` (left of the input) and
 * `footer` (under it). The DeepSeek client puts the mode, model and thinking chips
 * in the prefix and the queue plus the usage line in the footer; this file is the
 * same split, so the composer stays one framework component instead of a stack of
 * boxes next to one.
 *
 * Every picker writes the settings row the backend reads on each turn
 * (`agent.permission`, `chat.model`, the per-session thinking key) — nothing is
 * mirrored client-side.
 */

export const PERMISSION_VALUES = ['read-only', 'workspace', 'full'] as const;
export type PermissionValue = (typeof PERMISSION_VALUES)[number];

export interface ComposerSlotProps {
  sessionId: string;
  permission: string;
  model: string;
  thinkingMode: string;
  onSetting: (key: string, value: string) => void;
  running?: boolean;
  /** Live output rate while a turn streams ("232"), measured from the deltas. */
  rate?: string;
  onInterject?: (text: string) => void;
  /**
   * The role this session selected, and a stable code when the build cannot
   * resolve it (`unknown-preset`). Both come from the session list row — this is
   * not a second read. A broken role leaves the session with no tools and every
   * call refused, so the reason belongs where the conversation is
   * (2026-11-26 audit, 高); a healthy session renders nothing extra.
   */
  presetId?: string;
  presetProblem?: string;
  usage?: {
    turns?: number;
    steps?: number;
    tokens?: number;
    cached?: number;
    prompt?: number;
    context?: number;
    window?: number;
  };
}

/**
 * The settings + context the composer needs, read from the same domains the rest of
 * the shell uses. One hook so the thread and the landing composer cannot drift.
 */
export function useComposerSettings(sessionId: string) {
  const [rows, setRows] = useState<SettingsRows>({});
  const [context, setContext] = useState<ContextView | null>(null);

  useEffect(() => subscribeSettings((value) => setRows(value)), []);

  useEffect(() => {
    let cancelled = false;
    let off: (() => void) | undefined;
    if (!sessionId) return;
    void subscribeContext(sessionId, (next) => {
      if (!cancelled) setContext(next.context);
    })
      .then((unsubscribe) => {
        if (cancelled) unsubscribe();
        else off = unsubscribe;
      })
      .catch(() => {
        if (!cancelled) setContext(null);
      });
    return () => {
      cancelled = true;
      off?.();
    };
  }, [sessionId]);

  // No session means no context to fold: derived, never written from an effect.
  const activeContext = sessionId ? context : null;
  const model = String(rows[SETTING_KEYS.chatModel] ?? '');
  const permission = String(rows[SETTING_KEYS.agentPermission] ?? 'workspace');
  const thinkingMode = model && sessionId ? String(rows[thinkingSettingKey(sessionId, model)] ?? '') : '';
  const lastRequest = activeContext?.requests?.[activeContext.requests.length - 1];
  return {
    permission,
    model,
    thinkingMode,
    usage: {
      turns: activeContext?.snapshot?.history?.user,
      steps: activeContext?.requests?.length,
      tokens: activeContext?.requests?.reduce((sum, request) => sum + (request.total ?? 0), 0),
      cached: lastRequest?.cached,
      prompt: lastRequest?.prompt,
      context: activeContext?.used,
      window: activeContext?.window,
    },
  };
}

/** The thinking settings key of one session and model (backend-owned format). */
export function thinkingSettingKey(sessionId: string, model: string): string {
  return `session.${sessionId}.model.${model}.thinking`;
}

/** `Sender.prefix`: the permission preset, the model and its thinking effort. */
export function ComposerPickers(props: ComposerSlotProps) {
  const { t, locale } = useI18n();
  const [models, setModels] = useState<ModelRow[]>([]);

  useEffect(() => {
    let cancelled = false;
    void loadModels()
      .then((view) => {
        if (!cancelled) setModels(view.models);
      })
      .catch(() => {
        // A catalog that cannot be read leaves the picker absent, not broken.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const current = models.find((row) => row.id === props.model);
  const modes = current?.thinkingModes ?? [];

  return (
    <div className="zk-composer-pickers" data-testid="composer-pickers">
      {/* The diagnosis half of a broken role: the gate refuses every call
          fail-closed, and without this line the conversation simply looks dead.
          Warning, not error: it is a state the user fixes by picking a role. */}
      {props.presetProblem ? (
        <Alert
          type="warning"
          showIcon
          role="alert"
          data-testid="composer-preset-problem"
          title={t('session.preset.problem.title', { presetId: props.presetId ?? '' })}
          description={t('session.preset.problem.help')}
        />
      ) : null}
      <Space size={2} wrap>
        <Select
          size="small"
          variant="borderless"
          popupMatchSelectWidth={false}
          value={
            (PERMISSION_VALUES as readonly string[]).includes(props.permission)
              ? (props.permission as PermissionValue)
              : undefined
          }
          onChange={(value: PermissionValue) => props.onSetting(SETTING_KEYS.agentPermission, value)}
          options={PERMISSION_VALUES.map((value) => ({ value, label: t(`composer.permission.${value}`) }))}
          data-testid="composer-permission"
        />
        <Select
          size="small"
          variant="borderless"
          popupMatchSelectWidth={false}
          value={props.model || undefined}
          placeholder={t('composer.model.placeholder')}
          onChange={(value: string) => props.onSetting(SETTING_KEYS.chatModel, value)}
          options={models.map((row) => ({ value: row.id, label: row.name || row.id }))}
          data-testid="composer-model"
        />
        {modes.length > 1 && props.sessionId ? (
          <Select
            size="small"
            variant="borderless"
            popupMatchSelectWidth={false}
            value={props.thinkingMode || modes[0]?.id}
            onChange={(value: string) =>
              props.onSetting(thinkingSettingKey(props.sessionId, props.model), value)
            }
            options={modes.map((mode) => ({
              value: mode.id,
              label: locale === 'en' ? mode.nameEN || mode.nameZh : mode.nameZh || mode.nameEN,
            }))}
            data-testid="composer-thinking"
          />
        ) : null}
      </Space>
    </div>
  );
}

/** `Sender.footer`: the session usage line (the queue has its own B13 panel). */
export function ComposerFooter(props: ComposerSlotProps) {
  const t = useT();
  const usage = props.usage ?? {};
  const contextPercent =
    usage.context && usage.window ? Math.min(100, Math.round((usage.context / usage.window) * 100)) : undefined;
  if (usage.turns === undefined && !props.rate) return null;

  return (
    <div className="zk-composer-meters" data-testid="composer-meters">
      {props.rate ? (
        <Typography.Text type="secondary" data-testid="composer-rate">
          {t('composer.rate', { rate: props.rate })}
        </Typography.Text>
      ) : null}
      <Typography.Text type="secondary" data-testid="composer-usage">
        {t('composer.usage', {
          turns: usage.turns ?? 0,
          steps: usage.steps ?? 0,
          tokens: formatCompact(usage.tokens ?? 0),
          cached: usage.prompt ? Math.round(((usage.cached ?? 0) / usage.prompt) * 100) : 0,
          context: contextPercent ?? 0,
        })}
      </Typography.Text>
    </div>
  );
}

/** 12956 → "13.0k"; small numbers stay exact. */
export function formatCompact(value: number): string {
  if (value < 1000) return String(value);
  if (value < 1_000_000) return `${(value / 1000).toFixed(1)}k`;
  return `${(value / 1_000_000).toFixed(1)}M`;
}