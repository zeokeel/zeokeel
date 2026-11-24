import { useEffect, useState } from 'react';
import { Button, Popover, Select, Space, Tag, Typography } from 'antd';
import {
  loadModels,
  type ModelRow,
} from '@/api/read-models';
import { removeQueuedPrompt, subscribeContext, subscribePrompts, subscribeSettings } from '@/api/gateway';
import type { SettingsRows } from '@/api/gateway';
import type { ContextView } from '@/api/context';
import type { QueuedPrompt } from '@/api/prompt-queue';
import { useI18n } from '@/i18n';
import { SETTING_KEYS } from '@/design/settings-keys';

/**
 * The composer's small components: the mode (permission), model and thinking
 * pickers, the prompt queue, and the session usage line — the same information the
 * DeepSeek client puts around its input box.
 *
 * Every control writes the settings row the backend already reads on each turn
 * (`agent.permission`, `chat.model`, the per-session thinking key), so switching is
 * immediate and nothing is mirrored client-side.
 */

export const PERMISSION_VALUES = ['read-only', 'workspace', 'full'] as const;
export type PermissionValue = (typeof PERMISSION_VALUES)[number];

export interface ComposerStatusProps {
  sessionId: string;
  /** Current values, from the settings snapshot the shell already holds. */
  permission: string;
  model: string;
  thinkingMode: string;
  /** Write one settings row (agent.permission / chat.model / thinking key). */
  onSetting: (key: string, value: string) => void;
  /** Deliver a message immediately, interrupting the active run ("插话"). */
  onInterject: (text: string) => void;
  running: boolean;
  /** The session usage line: turns, steps, tokens, cached share, context %. */
  usage?: { turns?: number; steps?: number; tokens?: number; cached?: number; prompt?: number; context?: number; window?: number };
}

/**
 * The settings + context the composer bar needs, read from the same domains the rest
 * of the shell uses. One hook so the thread and the landing composer cannot drift.
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

export function ComposerStatus(props: ComposerStatusProps) {
  const { t, locale } = useI18n();
  const [models, setModels] = useState<ModelRow[]>([]);
  const [queued, setQueued] = useState<QueuedPrompt[]>([]);

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

  useEffect(() => {
    let cancelled = false;
    let off: (() => void) | undefined;
    if (!props.sessionId) return;
    void subscribePrompts(props.sessionId, (items) => {
      if (!cancelled) setQueued(items);
    })
      .then((unsubscribe) => {
        if (cancelled) unsubscribe();
        else off = unsubscribe;
      })
      .catch(() => {
        if (!cancelled) setQueued([]);
      });
    return () => {
      cancelled = true;
      off?.();
    };
  }, [props.sessionId]);

  const current = models.find((row) => row.id === props.model);
  const modes = current?.thinkingModes ?? [];
  // An empty session has no queue: derived, so no setState in an effect.
  const items = props.sessionId ? queued : [];
  const usage = props.usage ?? {};
  const contextPercent =
    usage.context && usage.window ? Math.min(100, Math.round((usage.context / usage.window) * 100)) : undefined;

  const queueContent = (
    <div className="zk-queue">
      <ul className="zk-queue__list">
        {items.map((item) => (
          <li key={item.id} className="zk-queue__row">
            <Typography.Text ellipsis style={{ maxWidth: 320 }}>
              {item.text}
            </Typography.Text>
            <Space size={4}>
              {props.running ? (
                <Button
                  size="small"
                  type="text"
                  onClick={() => {
                    void removeQueuedPrompt(props.sessionId, item.id);
                    props.onInterject(item.text);
                  }}
                  data-testid="queue-interject"
                >
                  {t('composer.queue.interject')}
                </Button>
              ) : null}
              <Button
                size="small"
                type="text"
                onClick={() => void removeQueuedPrompt(props.sessionId, item.id)}
                data-testid="queue-remove"
              >
                {t('composer.queue.remove')}
              </Button>
            </Space>
          </li>
        ))}
      </ul>
      <Typography.Text type="secondary">{t('composer.queue.hint')}</Typography.Text>
    </div>
  );

  return (
    <div className="zk-composer-status" data-testid="composer-status">
      <Space size={6} wrap>
        <Select
          size="small"
          variant="borderless"
          popupMatchSelectWidth={false}
          value={(PERMISSION_VALUES as readonly string[]).includes(props.permission)
            ? (props.permission as PermissionValue)
            : undefined}
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
        {items.length > 0 ? (
          <Popover trigger="click" placement="topLeft" content={queueContent} title={t('composer.queue.title')}>
            <Button size="small" type="text" icon={<Tag color="blue">{items.length}</Tag>} data-testid="composer-queue">
              {t('composer.queue.chip')}
            </Button>
          </Popover>
        ) : null}
      </Space>
      <Space size={8}>
        {usage.turns !== undefined ? (
          <Typography.Text type="secondary" data-testid="composer-usage">
            {t('composer.usage', {
              turns: usage.turns ?? 0,
              steps: usage.steps ?? 0,
              tokens: formatCompact(usage.tokens ?? 0),
              cached: usage.prompt ? Math.round(((usage.cached ?? 0) / usage.prompt) * 100) : 0,
              context: contextPercent ?? 0,
            })}
          </Typography.Text>
        ) : null}
      </Space>
    </div>
  );
}

/** 12956 → "13.0k"; small numbers stay exact. */
export function formatCompact(value: number): string {
  if (value < 1000) return String(value);
  if (value < 1_000_000) return `${(value / 1000).toFixed(1)}k`;
  return `${(value / 1_000_000).toFixed(1)}M`;
}
