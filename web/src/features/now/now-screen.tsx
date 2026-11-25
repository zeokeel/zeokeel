import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router';
import { Composer } from '@/chat/composer';
import { ComposerFooter, ComposerPickers, useComposerSettings } from '@/chat/composer-status';
import { rememberStaged, uploadBlob, type StagedAttachment } from '@/api/blobs';
import { setSetting } from '@/api/gateway';
import type { SlashCommand } from '@/chat/slash-commands';
import { ApprovalDock } from '@/features/approvals/approval-dock';
import { AskDialog } from '@/features/thread/ask-dialog';
import { useSessions } from '@/features/sessions/sessions-context';
import { EmptyState } from '@/ui/empty-state';
import { useT } from '@/i18n';

/**
 * R1 · Now — the landing surface (and the only content of `/`).
 *
 * One job: take a first message. Sending creates the session and moves to the
 * thread with the text in router state, which is the browser equivalent of the
 * native "first send creates a session" behaviour (home/composer.go:1-6).
 */
export function NowScreen() {
  const t = useT();
  const navigate = useNavigate();
  const { create } = useSessions();
  const composerSettings = useComposerSettings('');
  const [draft, setDraft] = useState('');
  const [attachments, setAttachments] = useState<StagedAttachment[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [focusRequest, setFocusRequest] = useState(0);
  // A cold load left focus on <body>: the user could type and nothing happened
  // until they clicked the field (round-5 loop audit F5). Focusing once, after
  // mount, is the whole fix; the guard keeps it away from touch screens where
  // the keyboard would cover the page.
  useEffect(() => {
    const fine = window.matchMedia?.('(hover: hover)').matches ?? true;
    if (!fine) return;
    const timer = window.setTimeout(() => setFocusRequest((value) => value + 1), 0);
    return () => window.clearTimeout(timer);
  }, []);
  const [notice, setNotice] = useState<string | null>(null);

  const commands: SlashCommand[] = [
    { name: 'new', description: t('palette.newSession'), run: () => undefined, keepDraft: true },
    { name: 'inbox', description: t('now.command.inbox'), run: () => navigate('/inbox') },
    { name: 'settings', description: t('now.command.settings'), run: () => navigate('/settings/model') },
    {
      name: 'capabilities',
      description: t('now.command.capabilities'),
      run: () => navigate('/capabilities'),
    },
    { name: 'clear', description: t('now.command.clear'), run: () => setDraft(''), keepDraft: true },
  ];

  const send = async () => {
    const text = draft.trim();
    if (!text || creating) return;
    if (attachments.some((item) => item.uploading)) return;
    setCreating(true);
    setError(null);
    // Sending from the landing page creates a session first: that await owns the
    // whole 220–584ms a cold start takes, and before this the only feedback was
    // a greyed composer (round-4 loop audit F1). The note says what is happening.
    setNotice(t('now.creatingSession'));
    try {
      // Send only what finished uploading: a provisional chip has no blob id.
      const ready = attachments.filter((item) => !item.uploading);
      // The optimistic bubble needs the names: without the staged metadata it
      // fell back to a bare 「附件」 until the snapshot landed (round-6 loop audit
      // R4-6). The thread composer has always done this; this one did not.
      const meta = ready.map(({ id, name, mime, bytes }) => ({ id, name, mime, bytes }));
      if (meta.length > 0) rememberStaged(meta);
      const session = await create();
      setDraft('');
      navigate(`/c/${session.id}`, {
        state: {
          pendingText: text,
          ...(ready.length > 0 ? { pendingAttachments: ready, attachmentMeta: meta } : {}),
        },
      });
      setAttachments([]);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('shell.createFailed'));
    } finally {
      setNotice(null);
      setCreating(false);
    }
  };

  return (
    <div className="zk-thread">
      <div className="zk-thread__body">
        <div className="zk-now__welcome">
        <EmptyState title={t('now.prompt.empty')} description={t('now.prompt.description')} />
        {/* Three concrete starts: an empty landing screen with one placeholder
            asks the user to invent the first move (round-2 composer audit). A
            click fills the composer and leaves the decision to edit or send. */}
        <div className="zk-now__suggestions" data-testid="now-suggestions">
          {(['explain', 'review', 'automate'] as const).map((key) => (
            <button
              key={key}
              type="button"
              className="zk-now__suggestion"
              data-testid={`now-suggestion-${key}`}
              onClick={() => {
                const text = t(`now.suggest.${key}`);
                // Never throw away what the user typed: append on a new line
                // instead of replacing, then put the caret in the composer.
                setDraft((current) => {
                  const trimmed = current.replace(/\s+$/, '');
                  return trimmed === '' ? text : `${trimmed}\n${text}`;
                });
                setFocusRequest((current) => current + 1);
              }}
            >
              {t(`now.suggest.${key}`)}
            </button>
          ))}
        </div>
        </div>
      </div>
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : null}
      <AskDialog />
      <ApprovalDock />
      <Composer
        prefix={
          <ComposerPickers
            sessionId=""
            permission={composerSettings.permission}
            model={composerSettings.model}
            thinkingMode=""
            onSetting={(key, value) => void setSetting(key, value)}
          />
        }
        footer={
          <ComposerFooter
            sessionId=""
            permission={composerSettings.permission}
            model={composerSettings.model}
            thinkingMode=""
            usage={composerSettings.usage}
            onSetting={(key, value) => void setSetting(key, value)}
          />
        }
        attachments={attachments}
        onAttachFiles={(files) => {
          void (async () => {
            for (const file of files) {
              // Same provisional chip as the thread composer: the landing page
              // used to show nothing for the whole upload, and a large file could
              // be sent before it finished — silently dropped (round-5 loop audit).
              const provisional = `uploading-${Math.random().toString(36).slice(2)}`;
              setAttachments((current) => [
                ...current,
                {
                  id: provisional,
                  name: file.name,
                  mime: file.type || 'application/octet-stream',
                  bytes: file.size,
                  uploading: true,
                },
              ]);
              try {
                const staged = await uploadBlob(file);
                // A successful attach means the earlier failure no longer
                // describes the state of the composer.
                setError(null);
                setAttachments((current) =>
                  current.map((item) => (item.id === provisional ? staged : item)),
                );
              } catch (cause) {
                setAttachments((current) => current.filter((item) => item.id !== provisional));
                setError(cause instanceof Error && cause.message === 'attachment_too_large'
                  ? t('composer.attachTooLarge')
                  : t('composer.attachFailed'));
              }
            }
          })();
        }}
        onRemoveAttachment={(id) =>
          setAttachments((current) => {
            // Release the preview object URL like the thread's composer does;
            // without it every attachment preview leaked until reload
            // (AUDIT-2026-11-24 C).
            const removed = current.find((item) => item.id === id);
            if (removed?.previewUrl) URL.revokeObjectURL(removed.previewUrl);
            return current.filter((item) => item.id !== id);
          })
        }
        value={draft}
        focusRequest={focusRequest}
        onChange={setDraft}
        onSubmit={() => void send()}
        // Starting a conversation is never blocked by an approval parked in some
        // other session; the dock above still shows it and links to its thread.
        // While the session is being created the note explains the pause; the
        // composer stays disabled with the send button visible. It must **not**
        // claim to be running: the stop button would call `onStop`, which has
        // nothing to stop yet (round-5 loop audit: a no-op stop and a hint that
        // promised "Esc 停止本轮" while the session was still being created).
        disabled={creating}
        notice={notice ?? undefined}
        commands={commands}
      />
    </div>
  );
}