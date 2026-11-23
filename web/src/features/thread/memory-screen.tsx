import { useEffect, useState } from 'react';
import { Empty, Spin, Typography } from 'antd';
import { useParams } from 'react-router';
import { loadMemory, type MemoryEntry } from '@/api/mock-surfaces';

/** R5 · Memory — what this session remembered. Read-only in this milestone. */
export function MemoryScreen() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const [entries, setEntries] = useState<MemoryEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    if (!sessionId) return;
    (async () => {
      try {
        const value = await loadMemory(sessionId);
        if (!cancelled) setEntries(value);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : '记忆加载失败');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">记忆</h1>
        <p className="zk-screen__note">这个会话里被记住的事实。</p>
      </header>
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : entries === null ? (
        <div className="zk-empty">
          <Spin />
        </div>
      ) : entries.length === 0 ? (
        <Empty description="这个会话还没有记忆条目。" />
      ) : (
        <ul className="zk-memory">
          {entries.map((entry) => (
            <li key={entry.id} className="zk-memory__row">
              <Typography.Text>{entry.text}</Typography.Text>
              {entry.createdAt ? (
                <Typography.Text type="secondary">{entry.createdAt}</Typography.Text>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}