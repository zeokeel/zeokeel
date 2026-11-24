import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Composer } from '@/chat/composer';
import { ComposerFooter, ComposerPickers, useComposerSettings } from '@/chat/composer-status';
import { setSetting } from '@/api/gateway';
import type { SlashCommand } from '@/chat/slash-commands';
import { ApprovalDock } from '@/features/approvals/approval-dock';
import { AskDialog } from '@/features/thread/ask-dialog';
import { useApprovals } from '@/features/approvals/approvals-context';
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
  const { approvals } = useApprovals();
  const approvalPending = approvals.length > 0;
  const composerSettings = useComposerSettings('');
  const [draft, setDraft] = useState('');
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
      navigate(`/c/${session.id}`, { state: { pendingText: text } });
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
        value={draft}
        onChange={setDraft}
        onSubmit={() => void send()}
        disabled={creating || approvalPending}
        placeholder={
          approvalPending ? t('composer.placeholder.approval') : t('composer.placeholder')
        }
        commands={commands}
      />
    </div>
  );
}