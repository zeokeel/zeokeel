import { useEffect, useState } from 'react';
import { Spin, Timeline, Typography } from 'antd';
import { useParams } from 'react-router';
import { loadSnapshot, type SnapshotMessage } from '@/api/gateway';
import { toolMeta } from '@/chat/tools/meta';
import { ZkIcon } from '@/design/icons';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';

interface Row {
  key: string;
  time: string;
  title: string;
  detail: string;
  status: 'success' | 'error';
}

/** R4 · Trajectory — the tool/reasoning timeline of one session. */
export function TrajectoryScreen() {
  const t = useT();
  const { sessionId } = useParams<{ sessionId: string }>();
  const [rows, setRows] = useState<Row[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    if (!sessionId) return;
    (async () => {
      try {
        const snapshot = await loadSnapshot(sessionId);
        if (cancelled) return;
        setRows(flatten(snapshot.messages, t));
      } catch (cause) {
        if (!cancelled) setError(describeError(cause, t, 'trajectory.loadFailed'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('thread.tabs.trajectory')}</h1>
        <p className="zk-screen__note">{t('trajectory.note')}</p>
      </header>
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : rows === null ? (
        <div className="zk-empty">
          <Spin />
        </div>
      ) : rows.length === 0 ? (
        <Typography.Text type="secondary">{t('trajectory.empty')}</Typography.Text>
      ) : (
        <Timeline
          items={rows.map((row) => ({
            key: row.key,
            color: row.status === 'error' ? 'red' : 'gray',
            children: (
              <div className="zk-trajectory__row">
                <div className="zk-trajectory__title">
                  <ZkIcon name={row.status === 'error' ? 'alert-triangle' : 'check'} />
                  <span>{row.title}</span>
                  <span className="zk-trajectory__time">{row.time}</span>
                </div>
                {row.detail ? <pre className="zk-tool-io">{row.detail}</pre> : null}
              </div>
            ),
          }))}
        />
      )}
    </div>
  );
}

function flatten(messages: SnapshotMessage[], t: ReturnType<typeof useT>): Row[] {
  const rows: Row[] = [];
  messages.forEach((message, messageIndex) => {
    (message.steps ?? []).forEach((step, stepIndex) => {
      const status = step.status === 'error' ? 'error' : 'success';
      rows.push({
        key: `${message.id}-${step.callId}-${stepIndex}`,
        time: `#${messageIndex + 1}`,
        title: `${t(toolMeta(step.name).labelKey)} · ${step.name}`,
        detail:
          step.error ??
          (step.output === undefined
            ? ''
            : typeof step.output === 'string'
              ? step.output
              : JSON.stringify(step.output)),
        status,
      });
    });
  });
  return rows;
}