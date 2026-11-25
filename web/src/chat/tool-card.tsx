import { Collapse } from 'antd';
import type { ToolStep } from '@/chat/types';
import { renderToolCard } from './tools/registry';
import { useT } from '@/i18n';
import { valueText } from './tools/sections';

/**
 * A tool call as one disclosure: compact row (icon, friendly name, status,
 * one-line summary) + card body (target → changes → output) + the raw arguments
 * every card shares.
 *
 * The body is dispatched by exact tool name; an unregistered tool gets the
 * generic card. Nothing here decides *whether* a tool call happened — that is
 * the transcript's job.
 */
export function ToolCardBody({ step }: { step: ToolStep }) {
  const t = useT();
  const rawArguments = valueText(step.input);
  // A denial is a decision, not a malfunction: the user said no, so the card
  // says so in the same neutral language as a stop.
  const denied =
    step.errorKind === 'denied' ||
    (step.error !== undefined && /denied by approval/i.test(step.error));
  // The wire carries the failure text as the step's *output*: the snapshot has
  // `summary` + `errorKind` and no separate error field, so preferring the
  // category put the word "network" where the real sentence belongs (round-6
  // tools audit D3/N1).
  const outputText =
    typeof step.output === 'string' && step.output.trim() !== '' ? step.output : null;
  // Only a failed call gets the alert: without this guard the *success* output
  // became the red box (round-6 final audit R1 — every successful card announced
  // its own output as an error).
  const failed = step.status === 'error' || step.status === 'denied';
  const failureText = !failed
    ? null
    : denied || step.status === 'denied'
      ? t('thread.tool.denied')
      : step.error
        ? step.error
        : step.errorKind
          ? // The wire has no separate error field: a failed call's summary *is*
            // the failure text, and the category is only the fallback.
            outputText ?? t('thread.tool.failedBy', { kind: step.errorKind })
          : outputText;

  return (
    <div className="zk-tool-card">
      {failureText ? (
        // B10: a failed call shows its error output, expandable like any other
        // section — and it is open by default because a failure is the thing
        // the reader came for. A refusal or a stop is *not* a failure: the tool
        // says so neutrally instead of painting rig's English sentence red
        // (round-4 tools audit D4/D12).
        <div className={denied ? 'zk-tool-notice' : 'zk-msg-error'} role={denied ? 'status' : 'alert'}>
          {failureText}
        </div>
      ) : null}
      {renderToolCard(step)}
      {rawArguments ? (
        <Collapse
          ghost
          size="small"
          items={[
            {
              key: 'raw',
              label: t('thread.rawArguments'),
              children: <pre className="zk-tool-io">{rawArguments}</pre>,
            },
          ]}
        />
      ) : null}
    </div>
  );
}