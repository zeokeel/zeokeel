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
  const failureText = denied
    ? t('thread.tool.denied')
    : step.error
      ? step.error
      : step.errorKind
        ? t('thread.tool.failedBy', { kind: step.errorKind })
        : null;

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