import { MarkdownBody } from './markdown';
import { ThoughtSteps } from './thought-steps';
import type { MessageStatus, ZeoMessage } from './types';

/**
 * The assistant row: reasoning + tool steps (one ThoughtChain), then the
 * markdown answer, then honest metadata.
 *
 * State language (docs/WEB-CLIENT.md §9.4): a running turn is the accent
 * "running" state, never warning; a stopped turn is neutral; only a real error
 * is red.
 */
export function AssistantBody({
  message,
  status,
}: {
  message: ZeoMessage;
  status: MessageStatus;
}) {
  const streaming = status === 'loading' || status === 'updating';
  const stopped = !streaming && message.stopReason === 'aborted' && !message.error;

  return (
    <>
      <ThoughtSteps reasoning={message.reasoning} steps={message.steps} streaming={streaming} />
      {message.text ? <MarkdownBody text={message.text} streaming={streaming} /> : null}
      {message.error ? (
        <div className="zk-msg-error" role="alert">
          {message.error.message}
          {message.error.code ? `（${message.error.code}）` : ''}
        </div>
      ) : null}
      {stopped ? <div className="zk-msg-stopped">已停止</div> : null}
      <MessageMeta message={message} status={status} />
    </>
  );
}

function MessageMeta({ message, status }: { message: ZeoMessage; status: MessageStatus }) {
  const usage = message.usage;
  const hasUsage =
    usage !== undefined && (usage.inputTokens !== undefined || usage.outputTokens !== undefined);
  // A turn with no body at all (an empty assistant message after a failed run)
  // must not leave a bare `end_turn` chip floating under the previous bubble:
  // the stop reason only means something next to actual content.
  const hasBody =
    message.text.trim() !== '' ||
    message.reasoning.trim() !== '' ||
    message.steps.length > 0 ||
    message.error !== undefined;
  // The wire's stopReason (`stop`, `end_turn`, …) is a protocol value, not
  // product copy: showing it raw leaked "stop" into the finished row. The only
  // stop the user must see is an abort, and AssistantBody renders that as
  // 已停止;  everything else is visible from the turn itself.
  // Why: a running turn already has the composer's run state; a settled one
  // shows usage only when the gateway actually reported it (never `0`).
  if (status === 'loading' || !hasBody || !hasUsage) return null;

  return (
    <div className="zk-msg-meta">
      {hasUsage ? (
        <span>
          {formatTokens(usage.inputTokens)} 入 · {formatTokens(usage.outputTokens)} 出
        </span>
      ) : null}
    </div>
  );
}

function formatTokens(value: number | undefined): string {
  if (value === undefined) return '—';
  return value >= 1000 ? `${(value / 1000).toFixed(1)}k` : String(value);
}