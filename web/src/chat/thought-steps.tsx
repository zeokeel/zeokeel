import { useState } from 'react';
import { Think, ThoughtChain } from '@ant-design/x';
import type { ThoughtChainItemType } from '@ant-design/x';
import { ZkIcon } from '@/design/icons';
import { ToolCardBody } from './tool-card';
import { useT } from '@/i18n';
import { toolMeta } from './tools/meta';
import type { ToolStep } from './types';

/**
 * Reasoning and tool steps share one visual model (ThoughtChain) — the
 * composition rule from the antd-x skill: a tool step is a custom node, not a
 * parallel list with its own styling.
 *
 * Expansion defaults: running and failed nodes open (a failure must be read),
 * settled nodes closed, and a reader's explicit toggle always wins
 * (docs/WEB-CLIENT.md §7.3).
 */
export function ThoughtSteps({
  reasoning,
  steps,
  streaming,
  durationMs,
}: {
  reasoning: string;
  steps: ToolStep[];
  streaming: boolean;
  /** The turn's measured wall time, when the backend reported one. */
  durationMs?: number;
}) {
  const t = useT();
  const [userExpanded, setUserExpanded] = useState<string[] | null>(null);

  const items: ThoughtChainItemType[] = [];
  // While the model is thinking, the live reasoning is X's `Think` (task-37 #5):
  // it owns the blinking in-progress affordance. Once the turn settles the same
  // text moves into the step list, which stays `ThoughtChain` — two different
  // surfaces (in-progress vs settled), one data source.
  const liveThink =
    streaming && reasoning ? (
      <div className="zk-thought">
        <Think
          // Streaming must be **visible**: a collapsed Think would hide exactly
          // what the user is waiting on, so expansion is pinned both ways.
          expanded
          onExpand={() => undefined}
          loading
          title={t('thread.reasoning')}
          className="zk-think"
        >
          <div className="zk-thinking-text">{reasoning}</div>
        </Think>
      </div>
    ) : null;

  if (streaming && reasoning) {
    // No `destroyOnHidden`: the content must survive being collapsed and the
    // hand-over to the chain below.
    return liveThink;
  }

  if (reasoning) {
    items.push({
      key: 'reasoning',
      title: t('thread.reasoning'),
      // The turn's measured wall time, the native 「用时 158ms」 line.
      ...(durationMs ? { desc: formatDuration(durationMs) } : {}),
      icon: <ZkIcon name="sparkles" />,
      status: streaming ? 'loading' : 'success',
      collapsible: true,
      content: <div className="zk-reasoning-text">{reasoning}</div>,
    });
  }
  steps.forEach((step) => items.push(toolItem(step, t)));

  if (items.length === 0) return null;

  const autoExpanded = items
    .filter((item) => item.status === 'loading' || item.status === 'error')
    .map((item) => item.key)
    .filter((key): key is string => typeof key === 'string');

  return (
    <div className="zk-thought">
      <ThoughtChain
        items={items}
        line="solid"
        expandedKeys={userExpanded ?? autoExpanded}
        onExpand={(keys) => setUserExpanded(keys.map(String))}
        classNames={{ itemHeader: 'zk-thought__header', itemContent: 'zk-thought__content' }}
      />
    </div>
  );
}

function toolItem(step: ToolStep, t: ReturnType<typeof useT>): ThoughtChainItemType {
  const meta = toolMeta(step.name);
  // Protocol status → the user's own words; never the raw enum.
  const state =
    step.status === 'running'
      ? t('thread.status.running')
      : step.status === 'ok'
        ? t('thread.status.done')
        : t('thread.status.failed');
  const summary = toolSummaryLine(step);

  return {
    key: `tool-${step.callId}`,
    title: (
      <span className="zk-tool-title">
        <ZkIcon name={meta.icon} />
        <span>{t(meta.labelKey)}</span>
        <span className="zk-tool-title__id" title={step.name}>
          {step.name}
        </span>
      </span>
    ),
    description: (
      <span className="zk-tool-text">
        {summary ? `${summary} · ` : ''}
        {state}
      </span>
    ),
    status: step.status === 'running' ? 'loading' : step.status === 'ok' ? 'success' : 'error',
    collapsible: true,
    content: <ToolCardBody step={step} />,
  };
}

/**
 * The wire carries `input` as the arguments **text** the model produced, so a
 * preview that treats it as an object sees nothing, and one that stringifies it
 * again shows `"{\"command\":…}"` — escaped quotes and `\uXXXX` instead of the
 * command. Parse once; a string that is not JSON stays a string.
 */
export function parsedToolInput(input: unknown): unknown {
  if (typeof input !== 'string') return input;
  const text = input.trim();
  if (!text.startsWith('{') && !text.startsWith('[')) return input;
  try {
    return JSON.parse(text);
  } catch {
    return input;
  }
}

/** One-line preview: the target of the call, clipped, from its arguments. */
function toolSummaryLine(step: ToolStep): string {
  if (!step.input) return '';
  const parsed = parsedToolInput(step.input);
  if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
    const input = parsed as Record<string, unknown>;
    for (const key of [
      'command',
      'path',
      'pattern',
      'query',
      'url',
      'description',
      'subject',
      'name',
    ]) {
      const value = input[key];
      if (typeof value === 'string' && value.trim() !== '') return clip(value);
    }
  }
  // Not an object we know how to read: show the arguments as they are. A string
  // is already text (never stringify it again); anything else is JSON.
  if (typeof parsed === 'string') return clip(parsed);
  try {
    return clip(JSON.stringify(parsed));
  } catch {
    return '';
  }
}

function clip(value: string): string {
  const flat = value.replace(/\s+/g, ' ').trim();
  return flat.length > 96 ? `${flat.slice(0, 93)}…` : flat;
}

/** 158 → "158ms", 2300 → "2.3s", 95000 → "1m35s" — the native 「用时」 form. */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m${seconds}s`;
}
