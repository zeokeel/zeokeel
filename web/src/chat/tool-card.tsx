import { Collapse } from 'antd';
import type { ToolStep } from '@/chat/types';
import { renderToolCard } from './tools/registry';
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
  const rawArguments = valueText(step.input);

  return (
    <div className="zk-tool-card">
      {step.error ? (
        // B10: a failed call shows its error output, expandable like any other
        // section — and it is open by default because a failure is the thing
        // the reader came for.
        <div className="zk-msg-error" role="alert">
          {step.error}
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
              label: '原始参数',
              children: <pre className="zk-tool-io">{rawArguments}</pre>,
            },
          ]}
        />
      ) : null}
    </div>
  );
}