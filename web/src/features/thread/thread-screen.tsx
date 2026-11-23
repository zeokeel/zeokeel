import { useEffect, useRef, useState } from 'react';
import { Spin } from 'antd';
import { useLocation, useNavigate } from 'react-router';
import { Composer } from '@/chat/composer';
import type { SlashCommand } from '@/chat/slash-commands';
import { Transcript } from '@/chat/transcript';
import { ApprovalDock } from '@/features/approvals/approval-dock';
import { useApprovals } from '@/features/approvals/approvals-context';
import { useSessions } from '@/features/sessions/sessions-context';
import { useEscClaim, useShortcut } from '@/keyboard/keyboard-provider';
import { useGatewayChat } from '@/runtime/use-gateway-chat';
import { EmptyState } from '@/ui/empty-state';
import { FindBar } from './find-bar';
import { PromptQueuePanel } from './prompt-queue-panel';
import { usePromptQueue } from './use-prompt-queue';
import { useT } from '@/i18n';

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
export function ThreadScreen({ sessionId }: { sessionId: string }) {
  const location = useLocation();
  const navigate = useNavigate();
  const t = useT();
  const { reload, create } = useSessions();
  const { approvals } = useApprovals();
  const queue = usePromptQueue(sessionId);
  const [draft, setDraft] = useState('');
  const [snapshotError, setSnapshotError] = useState<string | null>(null);
  const [findOpen, setFindOpen] = useState(false);
  const [findQuery, setFindQuery] = useState('');
  const [findCursor, setFindCursor] = useState(0);

  const chat = useGatewayChat(sessionId, {
    onSnapshotError: (cause) =>
      setSnapshotError(cause instanceof Error ? cause.message : t('thread.loadFailed')),
  });

  // Set while the current run is being stopped on purpose, so the settle
  // handler knows *why* it ended and keeps the queue instead of draining it.
  const stoppedByUser = useRef(false);

  const stopRun = () => {
    stoppedByUser.current = true;
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
  const pendingText = (location.state as { pendingText?: string } | null)?.pendingText;
  const pendingSent = useRef(false);
  useEffect(() => {
    if (!pendingText || pendingSent.current) return;
    if (chat.isDefaultMessagesRequesting) return;
    pendingSent.current = true;
    navigate(location.pathname, { replace: true, state: null });
    chat.onRequest({ sessionId, text: pendingText });
  }, [pendingText, chat, sessionId, navigate, location.pathname]);

  // Settle: refresh the session list (the title may have changed) and, unless
  // the user stopped the run, send the queue head. An abort keeps the queue.
  const wasRequesting = useRef(false);
  useEffect(() => {
    if (wasRequesting.current && !chat.isRequesting) {
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

  const send = () => {
    const text = draft.trim();
    if (!text) return;
    if (chat.isRequesting) {
      // A5/B13: a prompt typed during a run waits in the backend queue. If this
      // gateway serves the inbox as the WS `prompts` domain and the client is
      // not switched over yet, keep the text and say so — never drop it.
      setDraft('');
      void queue.enqueue(text);
      return;
    }
    setDraft('');
    chat.onRequest({ sessionId, text });
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
          />
        )}
      </div>

      <ApprovalDock currentSessionId={sessionId} />

      <PromptQueuePanel
        items={queue.items}
        running={chat.isRequesting}
        onCancel={(id) => void queue.remove(id)}
        onSendHead={() => void sendQueueHead()}
      />

      <Composer
        value={draft}
        onChange={setDraft}
        onSubmit={send}
        onStop={stopRun}
        running={chat.isRequesting}
        disabled={approvals.length > 0}
        placeholder={approvals.length > 0 ? t('composer.placeholder.approval') : undefined}
        commands={commands}
      />
    </div>
  );
}