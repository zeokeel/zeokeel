import { AbstractChatProvider, type TransformMessage } from '@ant-design/x-sdk';
import type { SSEOutput, XRequestOptions } from '@ant-design/x-sdk';
import { createRunRequest, type RunRequestClient } from '@/api/run-stream';
import {
  parseRunEvent,
  type DoneData,
  type ErrorData,
  type MessageDeltaData,
  type ReasoningDeltaData,
  type RunEvent,
  type RunRequest,
  type ToolResultData,
  type ToolStartData,
  type UsageData,
} from '@/api/protocol';
import { assistantMessage, type ToolStep, type ZeoMessage } from '@/chat/types';

/**
 * The gateway's protocol projected onto `useXChat`.
 *
 * `AbstractChatProvider` has exactly three abstract methods and that is all
 * this class implements (docs/plans/web-runtime-adapter.md §2.2). Transport is
 * the stock `XRequest`; state, per-conversation isolation, streaming
 * coalescing and abort all belong to x-sdk.
 */
export class GatewayChatProvider extends AbstractChatProvider<ZeoMessage, RunRequest, SSEOutput> {
  constructor(request: RunRequestClient = createRunRequest()) {
    super({ request });
  }

  /** onRequest params → the POST body (`docs/TRANSPORT.md` §4.6). */
  transformParams(
    requestParams: Partial<RunRequest>,
    options: XRequestOptions<RunRequest, SSEOutput, ZeoMessage>,
  ): RunRequest {
    const merged = { ...(options.params ?? {}), ...requestParams };
    if (!merged.sessionId) throw new Error('chat run requires a sessionId');
    if (merged.editMessageId) {
      // §4.6: the edit's text IS the new content; empty would mean "delete".
      if (!merged.text || merged.text.trim() === '') {
        throw new Error('chat run requires text for an edit');
      }
      return {
        sessionId: merged.sessionId,
        text: merged.text,
        editMessageId: merged.editMessageId,
        clientRunId: merged.clientRunId ?? newClientRunId(),
      };
    }
    if (merged.regenerate) {
      // §4.6: `text` must be absent here — the gateway reruns the user message
      // it already holds, and a stray `text` is rejected with invalid_params.
      return {
        sessionId: merged.sessionId,
        clientRunId: merged.clientRunId ?? newClientRunId(),
        regenerate: true,
      };
    }
    if (!merged.text) throw new Error('chat run requires text');
    return {
      sessionId: merged.sessionId,
      text: merged.text,
      clientRunId: merged.clientRunId ?? newClientRunId(),
      ...(merged.attachments && merged.attachments.length > 0
        ? { attachments: merged.attachments }
        : {}),
    };
  }

  /**
   * What the user just said, shown immediately as the end-aligned bubble.
   *
   * A regenerate has no user turn: it produces an assistant row that streams the
   * new answer, so an empty user bubble would be a lie about the transcript.
   */
  transformLocalMessage(requestParams: Partial<RunRequest>): ZeoMessage {
    // An edit rewrites an existing row and a regenerate replaces the last
    // answer; neither adds a user bubble. The transcript is refreshed from the
    // gateway's `msg.reset`, never patched optimistically.
    if (requestParams.regenerate || requestParams.editMessageId) return assistantMessage();
    return { ...assistantMessage(), role: 'user', text: requestParams.text ?? '' };
  }

  /**
   * Fold one SSE chunk into the row. `onUpdate` calls this per event; the final
   * `onSuccess` calls it with `chunk === undefined` and every chunk in
   * `chunks`, which only matters when no update ever arrived.
   */
  transformMessage({ originMessage, chunk, chunks }: TransformMessage<ZeoMessage, SSEOutput>) {
    if (originMessage) {
      // Per-update call: fold the one chunk into the row we already have.
      return chunk ? applyChunk(originMessage, chunk) : originMessage;
    }
    // First update (or the final `onSuccess`, which hands over every chunk when
    // no update ever arrived). Never replay `chunks` onto an existing row: the
    // deltas are already in it, and replaying duplicates the answer.
    const base = assistantMessage();
    if (chunk) return applyChunk(base, chunk);
    return chunks.reduce<ZeoMessage>((acc, item) => applyChunk(acc, item), base);
  }
}

/**
 * `1023 capability_unsupported` with `data.name = "regenerate_not_supported"`
 * (§4.6). The client answers by disabling the menu entry — not by showing the
 * user a failed request they cannot do anything about.
 */
/** The five edit refusals, as the gateway names them in `data.name` (§4.6). */
export const EDIT_REFUSALS = [
  'edit_target_not_found',
  'edit_target_not_user',
  'edit_target_has_attachments',
  'edit_target_compacted',
  'edit_target_moved',
] as const;

export type EditRefusal = (typeof EDIT_REFUSALS)[number];

/** Read `data.name` out of a refusal the run stream reported. */
export function editRefusalOf(payload: unknown): EditRefusal | null {
  const data = (payload as { data?: { name?: string } } | null)?.data;
  const name = data?.name;
  return EDIT_REFUSALS.includes(name as EditRefusal) ? (name as EditRefusal) : null;
}

export function isRegenerateUnsupported(error: { message?: string }): boolean {
  const message = error.message ?? '';
  return message.includes('regenerate_not_supported') || message.includes('capability_unsupported');
}

export function applyChunk(base: ZeoMessage, chunk: SSEOutput): ZeoMessage {
  const event = parseRunEvent(chunk);
  if (!event) return base;
  return applyEvent(base, event);
}

function applyEvent(base: ZeoMessage, event: RunEvent): ZeoMessage {
  switch (event.name) {
    case 'message.delta': {
      const delta = event.data as MessageDeltaData | undefined;
      if (typeof delta?.text !== 'string') return withUnknown(base, event.rawName);
      return { ...base, text: base.text + delta.text };
    }
    case 'reasoning.delta': {
      const delta = event.data as ReasoningDeltaData | undefined;
      if (typeof delta?.text !== 'string') return withUnknown(base, event.rawName);
      return { ...base, reasoning: base.reasoning + delta.text };
    }
    case 'tool.start': {
      const tool = event.data as ToolStartData | undefined;
      if (!tool?.callId || !tool.name) return withUnknown(base, event.rawName);
      const step: ToolStep = {
        callId: tool.callId,
        name: tool.name,
        input: tool.input,
        status: 'running',
      };
      return { ...base, steps: [...base.steps, step] };
    }
    case 'tool.result': {
      const result = event.data as ToolResultData | undefined;
      if (!result?.callId) return withUnknown(base, event.rawName);
      const exists = base.steps.some((step) => step.callId === result.callId);
      const settled: ToolStep = {
        callId: result.callId,
        name: result.callId,
        input: undefined,
        output: result.output,
        error: result.error,
        status: result.ok ? 'ok' : 'error',
      };
      return {
        ...base,
        steps: exists
          ? base.steps.map((step) =>
              step.callId === result.callId
                ? {
                    ...step,
                    output: result.output,
                    error: result.error,
                    status: result.ok ? 'ok' : 'error',
                  }
                : step,
            )
          : // A result for a call we never saw start: keep it rather than
            // dropping information the user needs (no silent loss).
            [...base.steps, settled],
      };
    }
    case 'usage': {
      const usage = event.data as UsageData | undefined;
      if (!usage) return withUnknown(base, event.rawName);
      return { ...base, usage };
    }
    case 'done': {
      const done = event.data as DoneData | undefined;
      return { ...base, stopReason: done?.stopReason ?? 'end_turn' };
    }
    case 'error': {
      const error = event.data as ErrorData | undefined;
      if (!error?.message) return withUnknown(base, event.rawName);
      return {
        ...base,
        error: { code: error.code ?? 'error', message: error.message },
      };
    }
    default:
      return withUnknown(base, event.rawName || 'unknown');
  }
}

function withUnknown(base: ZeoMessage, name: string): ZeoMessage {
  const unknownEvents = base.unknownEvents ?? [];
  if (unknownEvents.includes(name)) return base;
  return { ...base, unknownEvents: [...unknownEvents, name] };
}

export function newClientRunId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `run-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}