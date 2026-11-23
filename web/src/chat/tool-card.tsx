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