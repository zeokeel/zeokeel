import { useEffect, useState } from 'react';
import { Spin } from 'antd';
import { connectionStatus, onConnectionStatus } from '@/api/gateway';
import type { ConnectionStatus } from '@/api/ws/rpc';
import { useT } from '@/i18n';

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
  const t = useT();

  useEffect(() => onConnectionStatus(setStatus), []);

  if (status === 'online') return null;

  if (status === 'unauthorized') {
    return (
      <span className="zk-connection zk-connection--stale" role="alert">
        <span>{t('shell.connection.stale')}</span>
        <button
          type="button"
          className="zk-connection__retry"
          onClick={() => window.location.reload()}
        >
          {t('shell.connection.reload')}
        </button>
      </span>
    );
  }

  return (
    <span className="zk-connection" role="status" aria-live="polite">
      {status === 'connecting' ? (
        <>
          <Spin size="small" />
          <span>{t('shell.connection.connecting')}</span>
        </>
      ) : (
        <>
          <span className="zk-connection__dot" aria-hidden="true" />
          <span>{t('shell.connection.offline')}</span>
        </>
      )}
    </span>
  );
}
