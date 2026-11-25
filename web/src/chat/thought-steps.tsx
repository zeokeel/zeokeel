import { useState, type ReactNode } from 'react';
import { Think, ThoughtChain } from '@ant-design/x';
import type { ThoughtChainItemType } from '@ant-design/x';
import { ZkIcon } from '@/design/icons';
import { ToolCardBody } from './tool-card';
import { useT } from '@/i18n';
import type { WorkStepsCollapse, WorkStepsMode } from './use-work-steps';
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
  reasoningSegments,
  steps,
  streaming,
  durationMs,
  iterations,
  mode = 'standard',
  collapse = 'completion',
  isLatest = true,
  parkedTool,
}: {
  reasoning: string;
  /** Per-iteration thinking, when the turn had more than one model round. */
  reasoningSegments?: Array<{ iteration: number; text: string; offset?: number }>;
  steps: ToolStep[];
  streaming: boolean;
  /** The turn's measured wall time, when the backend reported one. */
  durationMs?: number;
  /** Model iterations in the turn (one thinking/tool round each). */
  iterations?: number;
  /** 设置 → 外观 → 工作步骤展示 (concise / standard / detailed / expanded). */
  mode?: WorkStepsMode;
  /** 设置 → 外观 → 收起时机: fold on settle, or only when the next turn starts. */
  collapse?: WorkStepsCollapse;
  /** Whether this is the last row: "next-input" folds once a newer one exists. */
  isLatest?: boolean;
  /** A tool call waiting on the approval card: its node says 等待批准. */
  parkedTool?: { name: string; summary?: string };
}) {
  const t = useT();
  const [userExpanded, setUserExpanded] = useState<string[] | null>(null);
  // The live Think's own expansion: open while streaming unless the reader folds it.
  const [liveOpen, setLiveOpen] = useState<boolean | undefined>(undefined);

  const items: ThoughtChainItemType[] = [];
  // While the model is thinking, the live reasoning is X's `Think` (task-37 #5):
  // it owns the blinking in-progress affordance. Once the turn settles the same
  // text moves into the step list, which stays `ThoughtChain` — two different
  // surfaces (in-progress vs settled), one data source.
  const liveThink =
    streaming && reasoning ? (
      <div className="zk-thought">
        <Think
          destroyOnHidden={false}
          // Streaming is **visible** by default — a collapsed Think would hide
          // exactly what the user is waiting on — but the arrow is a real
          // control, not decoration: the reader may fold it while reading the
          // answer (round-4 think audit F-08).
          expanded={liveOpen ?? true}
          onExpand={(next) => setLiveOpen(next)}
          loading
          // Think renders its header as a plain div with an onClick; the title
          // carries the keyboard affordance instead (round-5 think audit #4).
          title={
            <button
              type="button"
              className="zk-thought__title-button"
              aria-expanded={liveOpen ?? true}
              onClick={(event) => {
                event.stopPropagation();
                setLiveOpen(!(liveOpen ?? true));
              }}
            >
              {t('thread.reasoning')}
            </button>
          }
          className="zk-think"
        >
          <div className="zk-thinking-text">{reasoning}</div>
        </Think>
      </div>
    ) : null;

  // While the model is thinking the live text stays above, but the steps must
  // stay visible too: the old early return hid a running tool (and the approval
  // that pauses the turn) behind the reasoning for the whole iteration, which
  // read as "nothing is happening" (round-4 loop audit).
  const stepsOnly = streaming && reasoning;
  if (stepsOnly) {
    // The parked call is the last running step with the approval's tool name.
    const parkedCallId = parkedTool
      ? [...steps].reverse().find((step) => step.name === parkedTool.name && step.status === 'running')
          ?.callId
      : undefined;
    const running = steps.map((step) => toolItem(step, t, step.callId === parkedCallId));
    if (running.length === 0) return liveThink;
    const defaultOpen = running
      .filter((item) => item.status === 'loading' || item.status === 'error')
      .map((item) => String(item.key));
    const openKeys = userExpanded ?? defaultOpen;
    const toggle = (key: string) =>
      setUserExpanded(
        openKeys.includes(key) ? openKeys.filter((item) => item !== key) : [...openKeys, key],
      );
    return (
      <div className="zk-thought">
        {liveThink}
        <ThoughtChain
          items={withTitleButtons(running, (key) => openKeys.includes(key), toggle)}
          line="solid"
          expandedKeys={openKeys}
          onExpand={(keys) => setUserExpanded(keys.map(String))}
          classNames={{ itemHeader: 'zk-thought__header', itemContent: 'zk-thought__content' }}
        />
      </div>
    );
  }

  // 简洁 shows what the user has to act on (a running or failed step) and hides
  // the successes behind one line; the other modes keep every step.
  const compactMode = mode === 'compact';
  const actionable = steps.filter((step) => step.status !== 'ok');
  const visibleSteps = compactMode && !streaming ? actionable : steps;
  const describedSteps = visibleSteps.map((step) =>
    toolItem(step, t, parkedTool?.name === step.name && step.status === 'running'),
  );

  // A multi-round turn keeps one node per round, interleaved with its tool cards
  // by position in the answer (round-5 tools audit D6). One segment is the
  // ordinary case and keeps the plain 思考 node.
  const segments = reasoningSegments?.filter((segment) => segment.text.trim() !== '') ?? [];
  if (segments.length > 1) {
    // Offsets alone do not order a turn that never wrote prose: every round
    // starts at text 0, so all offsets tie. Ties break by round — segment i, then
    // the i-th tool call (a tool call is what ends a round), then segment i+1 —
    // which is the true order in exactly that case.
    const merged: Array<{ offset: number; phase: number; item: ThoughtChainItemType }> = [];
    segments.forEach((segment, index) => {
      merged.push({
        offset: segment.offset ?? 0,
        phase: index * 2,
        item: {
          key: `thinking-${segment.iteration}`,
          title: t('thread.reasoning.round', { count: index + 1 }),
          icon: <ZkIcon name="sparkles" />,
          status: streaming ? 'loading' : 'success',
          collapsible: true,
          content: <div className="zk-reasoning-text">{segment.text}</div>,
        },
      });
    });
    visibleSteps.forEach((step, index) => {
      merged.push({ offset: step.offset ?? 0, phase: index * 2 + 1, item: describedSteps[index] });
    });
    merged.sort((left, right) =>
      left.offset === right.offset ? left.phase - right.phase : left.offset - right.offset,
    );
    merged.forEach((entry) => items.push(entry.item));
  } else if (reasoning) {
    items.push({
      key: 'reasoning',
      title: t('thread.reasoning'),
      // The turn's measured wall time and its round count, the native 「用时」
      // line plus how many model rounds it took.
      ...(durationMs || iterations
        ? {
            description: [durationMs ? formatDuration(durationMs) : null, iterations && iterations > 1 ? t('thread.reasoning.rounds', { count: iterations }) : null]
              .filter(Boolean)
              .join(' · '),
          }
        : {}),
      icon: <ZkIcon name="sparkles" />,
      status: streaming ? 'loading' : 'success',
      collapsible: true,
      content: <div className="zk-reasoning-text">{reasoning}</div>,
    });
  }
  if (segments.length <= 1) describedSteps.forEach((item) => items.push(item));

  const hiddenSuccesses = compactMode ? steps.length - visibleSteps.length : 0;
  if (compactMode && !streaming && hiddenSuccesses > 0) {
    // Nothing to act on: say how much work happened instead of listing it.
    items.push({
      key: 'concise-summary',
      title: t('thread.steps.summary', { count: hiddenSuccesses }),
      ...(durationMs ? { description: formatDuration(durationMs) } : {}),
      icon: <ZkIcon name="check" />,
      status: 'success',
      collapsible: false,
    });
  }

  if (items.length === 0) return null;

  const wantsAllOpen = mode === 'detailed' || mode === 'verbose';
  // `next-input` folds a settled turn only once a newer row exists; before that
  // it stays readable even after the run ends (it used to stay open forever,
  // because nothing told it that the conversation had moved on).
  const keepOpen = collapse === 'next-input' ? !streaming && isLatest : false;
  const expandedByMode = wantsAllOpen
    ? items
        .filter((item) => mode === 'verbose' || item.key !== 'reasoning')
        .map((item) => item.key)
        .filter((key): key is string => typeof key === 'string')
    : null;
  const autoExpanded = keepOpen
    ? items.map((item) => item.key).filter((key): key is string => typeof key === 'string')
    : expandedByMode
      ? expandedByMode
      : items
          .filter((item) => item.status === 'loading' || item.status === 'error')
          .map((item) => item.key)
          .filter((key): key is string => typeof key === 'string');

  const openKeys = userExpanded ?? autoExpanded;
  // antd X's chain header is a plain `div` with an onClick: no role, no
  // tabindex, no aria-expanded, so the whole step list was mouse-only
  // (round-4 think/tools audits). The title becomes a real button; it stops the
  // click from bubbling to the header so one press toggles once.
  const toggle = (key: string) => {
    setUserExpanded(
      openKeys.includes(key) ? openKeys.filter((item) => item !== key) : [...openKeys, key],
    );
  };
  const accessible = withTitleButtons(items, (key) => openKeys.includes(key), toggle);

  return (
    <div className="zk-thought">
      <ThoughtChain
        items={accessible}
        line="solid"
        expandedKeys={openKeys}
        onExpand={(keys) => setUserExpanded(keys.map(String))}
        classNames={{ itemHeader: 'zk-thought__header', itemContent: 'zk-thought__content' }}
      />
    </div>
  );
}

/**
 * antd X's chain header is a plain `div` with an onClick: no role, no tabindex,
 * no aria-expanded, so the step list was mouse-only (round-4 think/tools
 * audits). The title becomes a real button that stops the click from bubbling
 * to the header, so one press toggles once.
 */
function withTitleButtons(
  items: ThoughtChainItemType[],
  isOpen: (key: string) => boolean,
  toggle: (key: string) => void,
): ThoughtChainItemType[] {
  return items.map((item) => {
    const key = String(item.key);
    return {
      ...item,
      title: (
        <button
          type="button"
          className="zk-thought__title-button"
          aria-expanded={isOpen(key)}
          onClick={(event) => {
            event.stopPropagation();
            toggle(key);
          }}
        >
          {item.title as ReactNode}
        </button>
      ),
    };
  });
}

function toolItem(
  step: ToolStep,
  t: ReturnType<typeof useT>,
  parked = false,
): ThoughtChainItemType {
  const meta = toolMeta(step.name);
  // Protocol status → the user's own words; never the raw enum. A refused call
  // is a decision, not a failure (round-5 tools audit D4/D12).
  const denied = step.errorKind === 'denied' || step.status === 'denied';
  const cancelled = step.status === 'cancelled';
  const state = denied
    ? t('thread.status.denied')
    : cancelled
      ? t('thread.status.cancelled')
    : parked && step.status === 'running'
      ? // The call has not touched anything yet: it is on the approval card,
        // and "运行中" made the user think the network request had happened
        // (round-5 tools audit D2).
        t('thread.status.awaitingApproval')
      : step.status === 'running'
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
    status:
      step.status === 'running'
        ? parked
          ? // Parked on the approval card: neither spinning nor finished.
            'abort'
          : 'loading'
        : step.status === 'ok' || denied || cancelled
          ? 'success'
          : // An unclassified error is not yet known to be a failure: the
            // denial kind arrives a frame or two after the status, and the node
            // flashed red before turning 已拒绝 (round-6 final audit R2).
            step.errorKind
            ? 'error'
            : 'abort',
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
  // A todo list has no single substring: say how many items and how many are
  // done instead of dumping the JSON array into the row (round-5 tools audit D11).
  if (parsed !== null && typeof parsed === 'object' && Array.isArray((parsed as { todos?: unknown }).todos)) {
    const todos = (parsed as { todos: Array<Record<string, unknown>> }).todos;
    const done = todos.filter((item) => item?.status === 'completed' || item?.status === 'done').length;
    return `${done}/${todos.length}`;
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
