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
        steps: (message.steps ?? []).map((step) => ({
          callId: step.callId,
          name: step.name,
          input: step.input,
          output: step.output,
          error: step.error,
          status: (step.status === 'error'
            ? 'error'
            : step.status === 'running'
              ? 'running'
              : 'ok') satisfies ToolStepStatus,
        })),
        ...(message.attachments && message.attachments.length > 0
          ? { attachments: message.attachments }
          : {}),
        ...(message.usage ? { usage: message.usage } : {}),
        ...(message.stopReason ? { stopReason: message.stopReason } : {}),
        // A turn that failed is stored with its error text; a reload must
        // show that red line, not an empty assistant bubble.
        ...(message.remoteError
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
    refresh: async () => {
      const durable = await loadMessages();
      // Never replace a longer transcript with a shorter one: the snapshot can lag
      // a just-sent turn, and deleting the user's own message is worse than showing
      // the optimistic row a moment longer.
      if (durable.length >= (chat.messages?.length ?? 0)) {
        // `DefaultMessageInfo` carries the same fields the store holds; the cast is
        // the SDK's own conversion (MessageInfo is the same shape minus the
        // loader-only fields).
        chat.setMessages(durable as never);
      }
    },
  };
}