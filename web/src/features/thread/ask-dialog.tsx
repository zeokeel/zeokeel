import { useEffect, useRef, useState } from 'react';
import { Button, Input, Modal, Space, Typography } from 'antd';
import { answerAsk, cancelAsk, subscribeAsk, type AskPending } from '@/api/gateway';
import { useEscClaim } from '@/keyboard/keyboard-provider';
import { useT } from '@/i18n';

/**
 * The in-conversation ask (docs/AGENT-SURFACES.md, native 「需要你的确认」).
 *
 * The ask used to be a read-only row in the inbox: the model could ask, and nobody
 * could answer from the browser. This dialog is the answer path — options as
 * full-width buttons, 「其他…」 for a custom reply, 取消 to decline (the tool resolves
 * as cancelled and the run continues), Esc = 取消.
 *
 * It is a modal on purpose (the native client did the same): while the model waits
 * for an answer, nothing else in the conversation can make progress.
 */
export function AskDialog({ sessionId }: { sessionId?: string }) {
  const t = useT();
  const [pending, setPending] = useState<AskPending | null>(null);
  const [custom, setCustom] = useState(false);
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);

  // The domain pushes on every revision (a delta, a resync), not only when the
  // question changes: reset the draft only when the question is actually a new one,
  // otherwise a delta would wipe what the user is typing.
  const questionID = useRef<string | null>(null);
  useEffect(() => {
    const off = subscribeAsk((value) => {
      if ((value?.id ?? null) !== questionID.current) {
        questionID.current = value?.id ?? null;
        setCustom(false);
        setText('');
      }
      setPending(value);
    });
    return off;
  }, []);

  useEscClaim({
    id: 'ask-dialog',
    level: 'approval',
    active: pending !== null,
    onEscape: () => {
      if (!pending) return false;
      void cancelAsk(pending.id);
      setPending(null);
      return true;
    },
  });

  if (!pending) return null;
  const answer = async (value: string) => {
    setSending(true);
    try {
      await answerAsk(pending.id, value);
      setPending(null);
    } finally {
      setSending(false);
    }
  };

  return (
    <Modal
      open
      closable={false}
      title={t('ask.title')}
      mask={{ closable: false }}
      footer={
        <Space>
          <Button
            onClick={() => {
              void cancelAsk(pending.id);
              setPending(null);
            }}
          >
            {t('ask.cancel')}
          </Button>
          {custom ? (
            <Button
              type="primary"
              loading={sending}
              disabled={!text.trim()}
              onClick={() => void answer(text.trim())}
            >
              {t('ask.submit')}
            </Button>
          ) : null}
        </Space>
      }
      data-testid="ask-dialog"
      data-session-id={sessionId}
    >
      <Typography.Paragraph>{pending.text}</Typography.Paragraph>
      {custom ? (
        <Input.TextArea
          autoFocus
          aria-label={t('ask.field')}
          autoSize={{ minRows: 2, maxRows: 6 }}
          value={text}
          onChange={(event) => setText(event.target.value)}
          onPressEnter={(event) => {
            if (!event.shiftKey) {
              event.preventDefault();
              if (text.trim()) void answer(text.trim());
            }
          }}
        />
      ) : (
        <Space orientation="vertical" size={8} style={{ width: '100%' }}>
          {(pending.options ?? []).map((option) => (
            <Button
              key={option}
              block
              disabled={sending}
              onClick={() => void answer(option)}
              data-testid="ask-option"
            >
              {option}
            </Button>
          ))}
          <Button block disabled={sending} onClick={() => setCustom(true)}>
            {t('ask.other')}
          </Button>
        </Space>
      )}
      <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
        {t('ask.hint')}
      </Typography.Paragraph>
    </Modal>
  );
}
