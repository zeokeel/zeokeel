import { useMemo } from 'react';
import { useXChat } from '@ant-design/x-sdk';
import type { DefaultMessageInfo, SSEOutput } from '@ant-design/x-sdk';
import { loadSnapshot } from '@/api/gateway';
import type { RunRequest } from '@/api/protocol';
import { assistantMessage, type ToolStepStatus, type ZeoMessage } from '@/chat/types';
import {
  GatewayChatProvider,
  editRefusalOf,
  isRegenerateUnsupported,
  type EditRefusal,
} from './gateway-chat-provider';

/**
 * The conversation runtime: x-sdk's `useXChat` with our provider
 * (docs/plans/web-runtime-adapter.md §7.4).
 *
 * `defaultMessages` is the snapshot seam — x-sdk awaits it per conversation
 * key, so switching sessions needs no separate loading state.
 */
export function useGatewayChat(
  sessionId: string,
  options: {
    onSnapshotError?: (error: unknown) => void;
    /** The gateway answered "regenerate is not supported" (§4.6). */
    onRegenerateUnsupported?: () => void;
    /** The gateway refused an edit, and named why (§4.6). */
    onEditRefused?: (refusal: EditRefusal) => void;
  } = {},
) {
  const provider = useMemo(() => new GatewayChatProvider(), []);
  const reportSnapshotError = options.onSnapshotError;
  const reportRegenerateUnsupported = options.onRegenerateUnsupported;
  const reportEditRefused = options.onEditRefused;

  const loadMessages = async (): Promise<DefaultMessageInfo<ZeoMessage>[]> => {
    // x-sdk swallows a throwing loader and shows an empty conversation; a
    // load failure must not look like "this session has no messages".
    const snapshot = await loadSnapshot(sessionId).catch((cause: unknown) => {
      reportSnapshotError?.(cause);
      throw cause;
    });
    // A persisted turn keeps its reasoning, tool steps and usage, so a
    // refreshed thread renders the same tool cards it showed live.
    return snapshot.messages.map((message) => ({
      id: message.id,
      message: {
        ...assistantMessage(),
        role: message.role === 'user' ? 'user' : 'assistant',
        text: message.text,
        reasoning: message.reasoning ?? '',
        ...(message.reasoningSegments && message.reasoningSegments.length > 0
          ? { reasoningSegments: message.reasoningSegments }
          : {}),
        steps: (message.steps ?? []).map((step) => ({
          callId: step.callId,
          name: step.name,
          input: step.input,
          output: step.output,
          // The category travels with the step: without it a settled or reloaded
          // card could not tell a denial from a network failure (round-5 tools
          // audit D3/D4 — this whitelist silently dropped the field).
          ...(step.error ? { error: step.error } : {}),
          ...(step.errorKind ? { errorKind: step.errorKind } : {}),
          // The wire's four states survive the mapping: a stopped or refused
          // call must not arrive as a green 完成 (round-6 final audit N3).
          status: (step.status === 'error'
            ? 'error'
            : step.status === 'running'
              ? 'running'
              : step.status === 'cancelled'
                ? 'cancelled'
                : step.status === 'denied'
                  ? 'denied'
                  : 'ok') satisfies ToolStepStatus,
        })),
        ...(message.attachments && message.attachments.length > 0
          ? { attachments: message.attachments }
          : {}),
        ...(message.usage ? { usage: message.usage } : {}),
        ...(message.stopReason ? { stopReason: message.stopReason } : {}),
        // A turn that failed is stored with its error text; a reload must
        // show that red line, not an empty assistant bubble.
        // A stop is not a failure: the gateway records `error=cancelled`
        // (kind `cancelled`), and painting that red told the user their run had
        // failed when they had simply stopped it (round-4 loop audit F2).
        ...(message.remoteErrorKind === 'cancelled' || message.remoteError === 'cancelled'
          ? { stopReason: 'aborted' }
          : message.remoteError
            ? { error: { code: 'run_failed', message: message.remoteError } }
            : {}),
    } satisfies ZeoMessage,
    status: 'success',
    }));
  };

  const chat = useXChat<ZeoMessage, ZeoMessage, RunRequest, SSEOutput>({
    provider,
    conversationKey: sessionId,
    defaultMessages: loadMessages,
    // Shown while the first event of a turn is in flight: an empty assistant
    // bubble with the loading dots, never a fabricated progress bar.
    requestPlaceholder: assistantMessage(),
    // Keep whatever streamed in. x-sdk replaces the loading row with this
    // fallback, so returning a fresh empty message would delete the partial
    // answer the user already saw; an abort is neutral (never an error).
    requestFallback: (_params, { error, messageInfo }) => {
      const partial = messageInfo?.message ?? assistantMessage();
      if (error.name === 'AbortError') {
        return { ...partial, stopReason: 'aborted' };
      }
      const refusal = editRefusalOf((error as { cause?: unknown }).cause ?? error);
      if (refusal) {
        // A refusal is an explanation, not a failure: the row stays, the reason
        // is shown, and the menu entry disables instead of inviting a retry.
        reportEditRefused?.(refusal);
        return { ...partial, stopReason: 'aborted' };
      }
      if (isRegenerateUnsupported(error)) {
        // Not a failure: the build simply lacks the half. Report it so the menu
        // entry disables itself, and keep the transcript neutral.
        reportRegenerateUnsupported?.();
        return { ...partial, stopReason: 'aborted' };
      }
      return {
        ...partial,
        error: { code: 'request_failed', message: error.message },
      };
    },
  });

  // The durable view takes over once a turn settles: attachment names, settled tool
  // states and server-side edits are the truth, the optimistic row is not.
  return {
    ...chat,
    refresh: async (preserveStop = false) => {
      const durable = await loadMessages();
      const current = chat.messages ?? [];
      // A durable row says "success"; a row that is still streaming must stay
      // loading. Without this merge, any domain change during a run (the approval
      // that pauses the turn, a tool upsert, a settings echo) replaced the live row
      // with a settled one: the live thinking collapsed and the whole turn looked
      // finished while the composer still said 运行中 (round-3 loop audit).
      const live = new Map<string, (typeof current)[number]>(
        current
          .filter((info) => info.status === 'loading' && info.id !== undefined)
          .map((info) => [String(info.id), info]),
      );
      // The row the stop applies to is the last assistant row: a snapshot read
      // before the cancel committed still says end_turn there, and matching by id
      // alone missed it because the streaming row's id can differ from the
      // durable one.
      let lastAssistantId: string | undefined;
      for (let index = durable.length - 1; index >= 0; index -= 1) {
        if (durable[index]?.message.role === 'assistant') {
          lastAssistantId = durable[index].id === undefined ? undefined : String(durable[index].id);
          break;
        }
      }
      const abortedIds = preserveStop && lastAssistantId ? new Set([lastAssistantId]) : new Set<string>();
      const merged = durable.map((row) => {
        // A stop the user just performed must survive a snapshot that was read
        // before the cancel committed: the row stays aborted, never end_turn.
        if (row.id !== undefined && abortedIds.has(String(row.id))) {
          return { ...row, message: { ...row.message, stopReason: 'aborted' } };
        }
        const streaming = row.id === undefined ? undefined : live.get(String(row.id));
        if (!streaming) return row;
        // The live row is ahead of the snapshot for what it already streamed.
        const longer = (a: string, b: string) => (a.length >= b.length ? a : b);
        return {
          ...row,
          status: 'loading',
          message: {
            ...row.message,
            text: longer(streaming.message.text, row.message.text),
            reasoning: longer(streaming.message.reasoning, row.message.reasoning),
          },
        };
      });
      // Never replace a longer transcript with a shorter one: the snapshot can lag
      // a just-sent turn, and deleting the user's own message is worse than showing
      // the optimistic row a moment longer.
      if (merged.length >= current.length) {
        // `DefaultMessageInfo` carries the same fields the store holds; the cast is
        // the SDK's own conversion (MessageInfo is the same shape minus the
        // loader-only fields).
        chat.setMessages(merged as never);
      }
    },
  };
}