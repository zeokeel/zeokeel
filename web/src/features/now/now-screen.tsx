import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Composer } from '@/chat/composer';
import { ComposerFooter, ComposerPickers, useComposerSettings } from '@/chat/composer-status';
import { uploadBlob, type StagedAttachment } from '@/api/blobs';
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
    setCreating(true);
    setError(null);
    try {
      const session = await create();
      setDraft('');
      navigate(`/c/${session.id}`, {
        state: { pendingText: text, ...(attachments.length > 0 ? { pendingAttachments: attachments } : {}) },
      });
      setAttachments([]);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('shell.createFailed'));
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="zk-thread">
      <div className="zk-thread__body">
        <EmptyState title={t('now.prompt.empty')} description={t('now.prompt.description')} />
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
              try {
                const staged = await uploadBlob(file);
                setAttachments((current) => [...current, staged]);
              } catch (cause) {
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
        onChange={setDraft}
        onSubmit={() => void send()}
        // Starting a conversation is never blocked by an approval parked in some
        // other session; the dock above still shows it and links to its thread.
        disabled={creating}
        commands={commands}
      />
    </div>
  );
}