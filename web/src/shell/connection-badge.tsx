import { useEffect, useState } from 'react';
import { Spin } from 'antd';
import { connectionStatus, onConnectionStatus } from '@/api/gateway';
import type { ConnectionStatus } from '@/api/ws/rpc';

/**
 * Connection state in the top band (docs/WEB-CLIENT.md §6, rule 7).
 *
 * A dropped control plane is *not* a warning: nothing needs the user's decision
 * and the reconnect is automatic, so this uses the neutral/accent vocabulary.
 * A rejected cookie is different — it is terminal and the user must act, so it
 * says what to do (reopen from the tray or the terminal) and offers a reload
 * instead of spinning forever.
 */
export function ConnectionBadge() {
  const [status, setStatus] = useState<ConnectionStatus>(connectionStatus());

  useEffect(() => onConnectionStatus(setStatus), []);

  if (status === 'online') return null;

  if (status === 'unauthorized') {
    return (
      <span className="zk-connection zk-connection--stale" role="alert">
        <span>授权链接已失效，请从托盘或终端重新打开</span>
        <button
          type="button"
          className="zk-connection__retry"
          onClick={() => window.location.reload()}
        >
          重新加载
        </button>
      </span>
    );
  }

  return (
    <span className="zk-connection" role="status" aria-live="polite">
      {status === 'connecting' ? (
        <>
          <Spin size="small" />
          <span>正在连接网关…</span>
        </>
      ) : (
        <>
          <span className="zk-connection__dot" aria-hidden="true" />
          <span>连接已断开，正在重连…</span>
        </>
      )}
    </span>
  );
}
