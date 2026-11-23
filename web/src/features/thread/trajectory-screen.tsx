import { useEffect, useState } from 'react';
import { Spin, Timeline, Typography } from 'antd';
import { useParams } from 'react-router';
import { loadSnapshot, type SnapshotMessage } from '@/api/gateway';
import { toolMeta } from '@/chat/tools/meta';
import { ZkIcon } from '@/design/icons';

interface Row {
  key: string;
  time: string;
  title: string;
  detail: string;
  status: 'success' | 'error';
}

/** R4 · Trajectory — the tool/reasoning timeline of one session. */
export function TrajectoryScreen() {
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
        setRows(flatten(snapshot.messages));
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : '轨迹加载失败');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">轨迹</h1>
        <p className="zk-screen__note">这一轮里 agent 调用了什么、结果如何。</p>
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
        <Typography.Text type="secondary">这个会话还没有工具调用。</Typography.Text>
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

function flatten(messages: SnapshotMessage[]): Row[] {
  const rows: Row[] = [];
  messages.forEach((message, messageIndex) => {
    (message.steps ?? []).forEach((step, stepIndex) => {
      const status = step.status === 'error' ? 'error' : 'success';
      rows.push({
        key: `${message.id}-${step.callId}-${stepIndex}`,
        time: `#${messageIndex + 1}`,
        title: `${toolMeta(step.name).label} · ${step.name}`,
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