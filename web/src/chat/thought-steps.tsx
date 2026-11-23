import { useState } from 'react';
import { ThoughtChain } from '@ant-design/x';
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
}: {
  reasoning: string;
  steps: ToolStep[];
  streaming: boolean;
}) {
  const t = useT();
  const [userExpanded, setUserExpanded] = useState<string[] | null>(null);

  const items: ThoughtChainItemType[] = [];
  if (reasoning) {
    items.push({
      key: 'reasoning',
      title: t('thread.reasoning'),
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

/** One-line preview: the target of the call, clipped, from its arguments. */
function toolSummaryLine(step: ToolStep): string {
  if (!step.input) return '';
  const input = step.input as Record<string, unknown>;
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
  try {
    return clip(JSON.stringify(step.input));
  } catch {
    return '';
  }
}

function clip(value: string): string {
  const flat = value.replace(/\s+/g, ' ').trim();
  return flat.length > 96 ? `${flat.slice(0, 93)}…` : flat;
}