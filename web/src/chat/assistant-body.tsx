import { Button } from 'antd';
import { useNavigate } from 'react-router';
import { classifyProviderFailure } from '@/features/provider-error';
import { MarkdownBody } from './markdown';
import { useT } from '@/i18n';
import { ThoughtSteps } from './thought-steps';
import { useWorkSteps } from './use-work-steps';
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
  isLatest = true,
  parkedTool,
}: {
  message: ZeoMessage;
  status: MessageStatus;
  /** No newer row follows this one (see `ui.workSteps.collapse`). */
  isLatest?: boolean;
  /** The call an approval is waiting on: its node says so instead of 运行中. */
  parkedTool?: { name: string; summary?: string };
}) {
  const t = useT();
  const workSteps = useWorkSteps();
  const streaming = status === 'loading' || status === 'updating';
  const stopped = !streaming && message.stopReason === 'aborted' && !message.error;

  return (
    <>
      <ThoughtSteps
        reasoning={message.reasoning}
        steps={message.steps}
        streaming={streaming}
        durationMs={message.usage?.durationMs}
        mode={workSteps.mode}
        collapse={workSteps.collapse}
        isLatest={isLatest}
        parkedTool={parkedTool}
      />
      {message.text ? <MarkdownBody text={message.text} streaming={streaming} /> : null}
      {/* A settled turn with no prose and no error used to leave a 0-height ghost
          row; the reader had no way to know whether the model had answered
          (round-4 reply audit). Say it, quietly. */}
      {!streaming && !message.text && !message.error && !stopped ? (
        <div className="zk-msg-empty">{t('thread.emptyAnswer')}</div>
      ) : null}
      {message.error ? <ProviderErrorRow error={message.error} /> : null}
      {stopped ? <div className="zk-msg-stopped">{t('thread.stopped')}</div> : null}
      <MessageMeta message={message} status={status} />
    </>
  );
}

/**
 * A provider failure, in the user's words, with the original one click away.
 *
 * v0.3.0 put the model client's report inline: an English JSON body inside a
 * Chinese conversation, with our internal code glued to the end. The first line
 * now says what to do (and offers the one action that helps), while the raw text
 * stays available — an unclassified error is still shown verbatim, because hiding
 * an error we do not understand is worse than showing it.
 */
function ProviderErrorRow({ error }: { error: { code: string; message: string } }) {
  const t = useT();
  const navigate = useNavigate();
  const failure = classifyProviderFailure(error.message);
  const sentence =
    failure.kind === 'noCredential'
      ? t('thread.providerError.noCredential')
      : failure.kind === 'unauthorized'
        ? t('thread.providerError.unauthorized')
        : failure.kind === 'unreachable'
          ? t('thread.providerError.unreachable')
          : null;
  const action =
    failure.kind === 'noCredential' || failure.kind === 'unauthorized' ? (
      <Button size="small" onClick={() => navigate('/settings')}>
        {t('thread.providerError.openSettings')}
      </Button>
    ) : null;

  return (
    <div className="zk-msg-error" role="alert">
      {sentence ? (
        <>
          <div className="zk-msg-error__sentence">{sentence}</div>
          {action}
        </>
      ) : (
        // Unknown: the raw text *is* the message.
        <div className="zk-msg-error__sentence">{failure.detail}</div>
      )}
      <details className="zk-msg-error__details">
        <summary>{t('thread.providerError.details')}</summary>
        <pre className="zk-msg-error__raw">
          {failure.provider ? `provider: ${failure.provider}\n` : ''}
          {failure.detail}
        </pre>
      </details>
    </div>
  );
}

function MessageMeta({ message, status }: { message: ZeoMessage; status: MessageStatus }) {
  const t = useT();
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
          {t('thread.usage', {
            in: formatTokens(usage.inputTokens),
            out: formatTokens(usage.outputTokens),
          })}
        </span>
      ) : null}
    </div>
  );
}

function formatTokens(value: number | undefined): string {
  if (value === undefined) return '—';
  return value >= 1000 ? `${(value / 1000).toFixed(1)}k` : String(value);
}