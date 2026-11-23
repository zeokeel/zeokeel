import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Composer } from '@/chat/composer';
import type { SlashCommand } from '@/chat/slash-commands';
import { ApprovalDock } from '@/features/approvals/approval-dock';
import { useApprovals } from '@/features/approvals/approvals-context';
import { useSessions } from '@/features/sessions/sessions-context';
import { EmptyState } from '@/ui/empty-state';

/**
 * R1 · Now — the landing surface (and the only content of `/`).
 *
 * One job: take a first message. Sending creates the session and moves to the
 * thread with the text in router state, which is the browser equivalent of the
 * native "first send creates a session" behaviour (home/composer.go:1-6).
 */
export function NowScreen() {
  const navigate = useNavigate();
  const { create } = useSessions();
  const { approvals } = useApprovals();
  const approvalPending = approvals.length > 0;
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const commands: SlashCommand[] = [
    { name: 'new', description: '新建会话并聚焦输入框', run: () => undefined, keepDraft: true },
    { name: 'inbox', description: '打开收件箱', run: () => navigate('/inbox') },
    { name: 'settings', description: '打开设置', run: () => navigate('/settings/model') },
    { name: 'capabilities', description: '打开能力', run: () => navigate('/capabilities') },
    { name: 'clear', description: '清空草稿', run: () => setDraft(''), keepDraft: true },
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
      setError(cause instanceof Error ? cause.message : '新建会话失败');
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="zk-thread">
      <div className="zk-thread__body">
        <EmptyState
          title="现在，说点什么"
          description="首条消息会创建一个新会话。"
        />
      </div>
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : null}
      <ApprovalDock />
      <Composer
        value={draft}
        onChange={setDraft}
        onSubmit={() => void send()}
        disabled={creating || approvalPending}
        placeholder={approvalPending ? '先处理上面的审批…' : '给 ZeoKeel 发消息…（Enter 发送）'}
        commands={commands}
      />
    </div>
  );
}