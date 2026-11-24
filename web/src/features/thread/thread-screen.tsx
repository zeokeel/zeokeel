import { useEffect, useRef, useState } from 'react';
import { Spin } from 'antd';
import { useLocation, useNavigate } from 'react-router';
import { Composer, resolveComposerPhase } from '@/chat/composer';
import type { SlashCommand } from '@/chat/slash-commands';
import { Transcript } from '@/chat/transcript';
import { ApprovalDock } from '@/features/approvals/approval-dock';
import { AskDialog } from '@/features/thread/ask-dialog';
import { rememberStaged, uploadBlob, type StagedAttachment } from '@/api/blobs';
import { formatRate } from '@/chat/stream-rate';
import { PlanDialog } from '@/features/thread/plan-dialog';
import { ComposerFooter, ComposerPickers, useComposerSettings } from '@/chat/composer-status';
import { useDraft } from '@/chat/use-draft';
import { cancelRun, setSetting } from '@/api/gateway';
import { useApprovals } from '@/features/approvals/approvals-context';
import { useSessions } from '@/features/sessions/sessions-context';
import { useEscClaim, useShortcut } from '@/keyboard/keyboard-provider';
import { useGatewayChat } from '@/runtime/use-gateway-chat';
import { EmptyState } from '@/ui/empty-state';
import { FindBar } from './find-bar';
import { PromptQueuePanel } from './prompt-queue-panel';
import { TaskChip } from './task-chip';
import { EditMessageDialog } from './edit-message-dialog';
import type { EditRefusal } from '@/runtime/gateway-chat-provider';
import { usePromptQueue } from './use-prompt-queue';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';

function focusComposer() {
  document.querySelector<HTMLTextAreaElement>('.zk-composer textarea')?.focus();
}

/**
 * R3 · Thread — the conversation surface.
 *
 * Three state owners, none copied between them: the transcript lives in x-sdk's
 * per-conversation store, the draft lives here, and run state is
 * `chat.isRequesting` (never a client-side timer or a fabricated percentage).
 *
 * Keyboard claims owned here: the find bar (L2) and stopping the run (L5). The
 * approval claim (L4) and the draft claim (L6) sit above and below, and the
 * arbiter picks by level, never by focus.
 */
const REFUSAL_KEY = {
  edit_target_not_found: 'thread.editRefusal.notFound',
  edit_target_not_user: 'thread.editRefusal.notUser',
  edit_target_has_attachments: 'thread.editRefusal.attachments',
  edit_target_compacted: 'thread.editRefusal.compacted',
  edit_target_moved: 'thread.editRefusal.moved',
} as const;

export function ThreadScreen({ sessionId }: { sessionId: string }) {
  const location = useLocation();
  const navigate = useNavigate();
  const t = useT();
  const { reload, create } = useSessions();
  const { approvals } = useApprovals();
  const sessionApprovalPending = approvals.some((request) => request.sessionId === sessionId);
  const queue = usePromptQueue(sessionId);
  // B11: the menu item is enabled unless the gateway says otherwise — either by
  // advertising no `regenerate` capability, or by answering 1023 on the first
  // attempt (a build whose backend lacks `SessionRegenerator`).
  const [regenerateUnsupported, setRegenerateUnsupported] = useState(false);
  // B11 edit: the target row, and the gateway's reason if it refused one.
  const [editTarget, setEditTarget] = useState<{ id: string; text: string } | null>(null);
  const [editRefusal, setEditRefusal] = useState<EditRefusal | null>(null);
  const draftStore = useDraft(sessionId);
  const draft = draftStore.value;
  const setDraft = draftStore.set;
  const [snapshotError, setSnapshotError] = useState<string | null>(null);
  const [findOpen, setFindOpen] = useState(false);
  const [findQuery, setFindQuery] = useState('');
  const [findCursor, setFindCursor] = useState(0);

  const chat = useGatewayChat(sessionId, {
    onSnapshotError: (cause) =>
      setSnapshotError(describeError(cause, t, 'thread.loadFailed')),
    onRegenerateUnsupported: () => setRegenerateUnsupported(true),
    onEditRefused: (refusal) => setEditRefusal(refusal),
  });

  // Set while the current run is being stopped on purpose, so the settle
  // handler knows *why* it ended and keeps the queue instead of draining it.
  const stoppedByUser = useRef(false);
  // The interject retry loop and the session it belongs to, so leaving the
  // conversation cancels it instead of posting into the wrong one.
  const interjectTimer = useRef<number | null>(null);
  const sessionRef = useRef(sessionId);
  useEffect(() => {
    sessionRef.current = sessionId;
    return () => {
      if (interjectTimer.current !== null) {
        window.clearTimeout(interjectTimer.current);
        interjectTimer.current = null;
      }
    };
  }, [sessionId]);

  const composerSettings = useComposerSettings(sessionId);
  // Live rate: characters streamed since the first frame of this turn, over wall
  // time. It is an estimate (no token counts arrive mid-turn) and says so.
  const firstFrameAt = useRef<number | null>(null);
  // The streaming text lives in the chat store, which changes on every delta: read
  // it through a ref so the sampling interval is created once per turn instead of
  // being torn down by each delta.
  const latestText = useRef(0);
  useEffect(() => {
    const last = chat.parsedMessages[chat.parsedMessages.length - 1];
    latestText.current = last?.message.text?.length ?? 0;
  }, [chat.parsedMessages]);
  const [rateText, setRateText] = useState('');
  useEffect(() => {
    if (!chat.isRequesting) {
      firstFrameAt.current = null;
      return;
    }
    const timer = setInterval(() => {
      const chars = latestText.current;
      if (chars === 0) {
        setRateText('');
        return;
      }
      if (firstFrameAt.current === null) firstFrameAt.current = Date.now();
      setRateText(formatRate({ chars, elapsedMs: Date.now() - firstFrameAt.current }));
    }, 400);
    return () => clearInterval(timer);
  }, [chat.isRequesting]);
  // Staged attachments: uploaded as soon as they are picked, so sending only names
  // their blob ids (TRANSPORT §6.4).
  const [attachments, setAttachments] = useState<StagedAttachment[]>([]);
  const [attachError, setAttachError] = useState<string | null>(null);
  const attachFiles = async (files: File[]) => {
    for (const file of files) {
      try {
        const staged = await uploadBlob(file);
        // A successful attach means the earlier failure no longer describes the
        // composer (the landing page already did this; the thread did not).
        setAttachError(null);
        setAttachments((current) => [...current, staged]);
      } catch (cause) {
        const tooLarge = cause instanceof Error && cause.message === 'attachment_too_large';
        setAttachError(tooLarge ? t('composer.attachTooLarge') : t('composer.attachFailed'));
      }
    }
  };
  const removeAttachment = (id: string) => {
    setAttachments((current) => {
      const target = current.find((item) => item.id === id);
      if (target?.previewUrl) URL.revokeObjectURL(target.previewUrl);
      return current.filter((item) => item.id !== id);
    });
  };

  const stopRun = () => {
    stoppedByUser.current = true;
    // `chat.abort()` only closes the client's stream: the gateway keeps running
    // and the next send is a 409 ("a run is active"). Cancel server-side first,
    // then drop the local stream so the UI reacts without waiting for the ack.
    void cancelRun(sessionId).catch(() => undefined);
    chat.abort();
  };

  // L5: a running turn is stopped by Esc from anywhere on the screen.
  useEscClaim({
    id: 'stop-run',
    level: 'stopRun',
    active: chat.isRequesting,
    onEscape: () => {
      stopRun();
      return true;
    },
  });

  // L2: the find bar sits above the transcript and below any overlay.
  useEscClaim({
    id: 'thread-find',
    level: 'findBar',
    active: findOpen,
    onEscape: () => {
      setFindOpen(false);
      focusComposer();
      return true;
    },
  });

  // K9: open the inspector's file page. No visible layout shift when it is
  // already open is acceptable (the route is the same).
  useShortcut({
    id: 'open-inspector-files',
    mod: true,
    alt: true,
    key: 'f',
    active: !location.pathname.includes('/files'),
    run: () => {
      navigate(`/c/${sessionId}/files`);
      return true;
    },
  });

  useShortcut({
    id: 'transcript-find',
    mod: true,
    key: 'f',
    active: !findOpen,
    run: () => {
      setFindOpen(true);
      return true;
    },
  });

  // Sending from `/` hands the text over in router state; send it exactly once,
  // then clear the state so a refresh cannot replay it.
  const handoff = (location.state as {
    pendingText?: string;
    pendingAttachments?: StagedAttachment[];
  } | null) ?? null;
  const pendingText = handoff?.pendingText;
  const pendingSent = useRef(false);
  useEffect(() => {
    if (!pendingText || pendingSent.current) return;
    if (chat.isDefaultMessagesRequesting) return;
    pendingSent.current = true;
    navigate(location.pathname, { replace: true, state: null });
    const staged = handoff?.pendingAttachments ?? [];
    chat.onRequest({
      sessionId,
      text: pendingText,
      ...(staged.length > 0 ? { attachments: staged.map((item) => item.id) } : {}),
    });
  }, [pendingText, handoff, chat, sessionId, navigate, location.pathname]);

  // Settle: refresh the session list (the title may have changed) and, unless
  // the user stopped the run, send the queue head. An abort keeps the queue.
  const wasRequesting = useRef(false);
  useEffect(() => {
    if (wasRequesting.current && !chat.isRequesting) {
      // The optimistic rows were placeholders: the durable snapshot is the truth
      // (attachment names, settled tool states, any server-side rewrite).
      void chat.refresh?.();
      void reload();
      const aborted = stoppedByUser.current;
      stoppedByUser.current = false;
      if (!aborted) {
        void (async () => {
          const list = await queue.refresh();
          const head = list[0];
          if (!head) return;
          await queue.remove(head.id);
          chat.onRequest({ sessionId, text: head.text });
        })();
      }
    }
    wasRequesting.current = chat.isRequesting;
    // eslint-disable-next-line react-hooks/exhaustive-deps -- settle runs once per transition
  }, [chat.isRequesting, reload]);

  /**
   * B11: regenerate goes through the same run endpoint with `regenerate:true`
   * and no text (§4.6) — never a second send path. One retry at a time.
   */
  const startEdit = (messageId: string, text: string) => setEditTarget({ id: messageId, text });

  /**
   * Commit an edit: same endpoint, third mode (§4.6). The transcript is *not*
   * patched here — the gateway rewrites history and pushes `msg.reset`.
   */
  const submitEdit = (text: string) => {
    const target = editTarget;
    setEditTarget(null);
    if (!target) return;
    chat.onRequest({ sessionId, editMessageId: target.id, text });
  };

  const regenerate = () => {
    if (chat.isRequesting) return;
    chat.onRequest({ sessionId, regenerate: true });
  };

  // One delivery rule for every text this screen sends (composer, plan approval):
  // while a run is active the gateway rejects a second one (409), so the text goes
  // into the backend queue and runs right after — the same path the composer takes.
  const deliver = (text: string): boolean => {
    if (!text.trim()) return false;
    if (chat.isRequesting) {
      // The queue carries text only (prompts.enqueue has no attachments field),
      // so sending attachments here would silently park them in the composer
      // until the next message picked them up — attaching them to a message the
      // user did not mean (AUDIT-2026-11-24 B-7). Refuse and say why.
      if (attachments.length > 0) {
        setAttachError(t('thread.composer.attachmentsWhileRunning'));
        return false;
      }
      // A failed enqueue used to be a void'd rejection with the draft already
      // cleared: the user's text vanished with no message (B-8).
      const pendingText = text;
      setDraft('');
      void queue
        .enqueue(pendingText)
        .then(() => setAttachError(null))
        .catch(() => {
          if (draftStore.value === '') setDraft(pendingText);
        });
      return true;
    }
    const ids = attachments.map((item) => item.id);
    const meta = attachments.map(({ id, name, mime, bytes }) => ({ id, name, mime, bytes }));
    rememberStaged(meta);
    attachments.forEach((item) => item.previewUrl && URL.revokeObjectURL(item.previewUrl));
    setAttachments([]);
    chat.onRequest({
      sessionId,
      text,
      ...(ids.length > 0 ? { attachments: ids, attachmentMeta: meta } : {}),
    });
    return true;
  };

  const send = () => {
    const text = draft.trim();
    if (!deliver(text)) return;
    setDraft('');
  };

  const sendQueueHead = async () => {
    const head = queue.items[0];
    if (!head || chat.isRequesting) return;
    await queue.remove(head.id);
    chat.onRequest({ sessionId, text: head.text });
  };

  const commands: SlashCommand[] = [
    {
      name: 'new',
      description: t('palette.newSession'),
      run: async () => {
        const session = await create();
        navigate(`/c/${session.id}`);
      },
    },
    { name: 'stop', description: t('thread.stopRun'), run: () => chat.abort(), keepDraft: true },
    { name: 'files', description: t('thread.openFiles'), run: () => navigate(`/c/${sessionId}/files`) },
    { name: 'inbox', description: t('now.command.inbox'), run: () => navigate('/inbox') },
    { name: 'settings', description: t('now.command.settings'), run: () => navigate('/settings/model') },
    { name: 'clear', description: t('now.command.clear'), run: () => setDraft(''), keepDraft: true },
  ];

  const matchIndexes =
    findOpen && findQuery.trim() !== ''
      ? chat.messages
          .map((info, index) => ({ info, index }))
          .filter(({ info }) => {
            const needle = findQuery.trim().toLowerCase();
            return (
              info.message.text.toLowerCase().includes(needle) ||
              info.message.reasoning.toLowerCase().includes(needle)
            );
          })
          .map(({ index }) => index)
      : [];

  const cursor = matchIndexes.length === 0 ? 0 : findCursor % matchIndexes.length;
  // The turn list decides connecting vs running: the function picks the
  // assistant frame itself, because the user's echo arrives first and a
  // "last message" reading would skip `connecting` entirely.
  const turnFrames = chat.messages.map((entry) => entry.message);
  const empty = chat.messages.length === 0 && !chat.isDefaultMessagesRequesting;

  return (
    <div className="zk-thread">
      {findOpen ? (
        <FindBar
          query={findQuery}
          onQueryChange={(value) => {
            setFindQuery(value);
            setFindCursor(0);
          }}
          matchCount={matchIndexes.length}
          activeMatch={cursor}
          onNext={() => setFindCursor((index) => index + 1)}
          onPrev={() => setFindCursor((index) => index - 1 + matchIndexes.length)}
          onClose={() => {
            setFindOpen(false);
            focusComposer();
          }}
        />
      ) : null}

      <div className="zk-thread__body">
        {snapshotError ? (
          <div className="zk-msg-error" role="alert">
            {t('thread.loadFailedDetail', { error: snapshotError })}
          </div>
        ) : chat.isDefaultMessagesRequesting ? (
          <div className="zk-empty">
            <Spin />
          </div>
        ) : empty ? (
          <EmptyState title={t('thread.start.title')} description={t('thread.start.description')} />
        ) : (
          <Transcript
            messages={chat.parsedMessages}
            sessionId={sessionId}
            focusIndex={matchIndexes.length > 0 ? matchIndexes[cursor] : undefined}
            onRegenerate={regenerate}
            regenerateDisabled={regenerateUnsupported}
            onEdit={startEdit}
            editDisabled={editRefusal !== null}
            editHint={editRefusal ? t(REFUSAL_KEY[editRefusal]) : undefined}
          />
        )}
      </div>

      <PlanDialog onContinue={deliver} />
      <AskDialog sessionId={sessionId} />
      <ApprovalDock currentSessionId={sessionId} />

      {/* B12: process-wide board (no sessionId) — tasks are not per-conversation
          unless the gateway scopes them. */}
      <TaskChip />

      {editTarget ? (
        <EditMessageDialog
          open
          initialText={editTarget.text}
          onCancel={() => setEditTarget(null)}
          onConfirm={submitEdit}
        />
      ) : null}

      <PromptQueuePanel
        items={queue.items}
        running={chat.isRequesting}
        error={queue.error}
        onCancel={(id) => {
          // Cancelling a queued prompt gives the text back: dropping it silently
          // was the one action in the queue that destroyed what the user typed
          // (round-2 composer audit: 队列回滚).
          const item = queue.items.find((entry) => entry.id === id);
          void queue.remove(id).then(() => {
            if (!item) return;
            const restored =
              draft.trim() === ''
                ? item.text
                : `${draft.replace(/\s+$/, '')}\n${item.text}`;
            setDraft(restored);
            focusComposer();
          });
        }}
        onSendHead={() => void sendQueueHead()}
      />

      {attachError ? (
        <div className="zk-msg-error" role="alert">
          {attachError}
        </div>
      ) : null}
      <Composer
        prefix={
          <ComposerPickers
            sessionId={sessionId}
            permission={composerSettings.permission}
            model={composerSettings.model}
            thinkingMode={composerSettings.thinkingMode}
            onSetting={(key: string, value: string) => void setSetting(key, value)}
          />
        }
        footer={
          <ComposerFooter
            sessionId={sessionId}
            permission={composerSettings.permission}
            model={composerSettings.model}
            thinkingMode={composerSettings.thinkingMode}
            running={chat.isRequesting}
            rate={chat.isRequesting ? rateText : ''}
            usage={composerSettings.usage}
            onSetting={(key: string, value: string) => void setSetting(key, value)}
            onInterject={(text: string) => {
              // 插话: abort, wait for the turn to actually end (the abort is a
              // request), then send. Sending into a live turn is a 409.
              stopRun();
              const deadline = Date.now() + 3000;
              const trySend = () => {
                interjectTimer.current = null;
                if (chat.isRequesting && Date.now() < deadline) {
                  interjectTimer.current = window.setTimeout(trySend, 100);
                  return;
                }
                // Switched conversation while waiting: send nothing, the old
                // session's run is already stopped (AUDIT-2026-11-24 C).
                if (sessionRef.current !== sessionId) return;
                chat.onRequest({ sessionId, text });
              };
              interjectTimer.current = window.setTimeout(trySend, 100);
            }}
          />
        }
        attachments={attachments}
        onAttachFiles={(files) => void attachFiles(files)}
        onRemoveAttachment={removeAttachment}
        value={draft}
        onChange={setDraft}
        onSubmit={send}
        onStop={stopRun}
        running={chat.isRequesting}
        // Three states: idle → connecting (submitted, no frame yet) → running.
        // The 30 s blackhole in v0.3.0 sat in "connecting" silently showing the
        // idle hint; now it says so.
        phase={resolveComposerPhase(chat.isRequesting, turnFrames)}
        // Only this conversation's approvals block its own composer: a request
        // parked in another session used to disable every input in the product
        // (round-2 F-3 / C-01).
        disabled={sessionApprovalPending}
        notice={sessionApprovalPending ? t('composer.notice.approval') : undefined}
        commands={commands}
      />
    </div>
  );
}