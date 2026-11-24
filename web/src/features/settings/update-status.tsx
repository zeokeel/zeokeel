import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Space, Tag, Typography } from 'antd';
import {
  checkForUpdate,
  downloadUpdate,
  fetchUpdateStatus,
  installUpdate,
  type UpdateStatus as UpdateStatusPayload,
} from '@/api/gateway';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';
import { MarkdownBody } from '@/chat/markdown';

/**
 * The version page's update surface (docs/TRANSPORT.md §5.1.12).
 *
 * The backend has checked GitHub Releases, verified and staged assets and
 * recorded install-on-restart plans since the modular shell, but nothing on the
 * wire reached it: the 关于 page was an empty section and the product simply
 * never updated. This component is that page.
 *
 * Deliberate choices:
 *  - a failed check is *shown*, never rendered as "已是最新";
 *  - "下载并安装" and "立即安装并重启" are separate verbs — restarting the app is
 *    the user's decision, not a side effect of pressing update;
 *  - a staged update is visible as staged, because that is what a restart does.
 */
export function UpdateStatus() {
  const t = useT();
  const [status, setStatus] = useState<UpdateStatusPayload | null>(null);
  const [busy, setBusy] = useState<'check' | 'download' | 'install' | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const next = await fetchUpdateStatus();
      setStatus(next);
      setFailure(null);
    } catch (cause) {
      setFailure(describeError(cause, t, 'settings.update.failed'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    // Deferred: the fetch settles asynchronously, and calling setState directly
    // in an effect body is what the lint rule (rightly) forbids.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  const run = useCallback(
    async (kind: 'check' | 'download' | 'install', action: () => Promise<UpdateStatusPayload>) => {
      setBusy(kind);
      setFailure(null);
      try {
        setStatus(await action());
      } catch (cause) {
        setFailure(describeError(cause, t, 'settings.update.failed'));
      } finally {
        setBusy(null);
      }
    },
    [t],
  );

  if (loading) {
    return <Typography.Text type="secondary">{t('settings.update.loading')}</Typography.Text>;
  }

  const staged = status?.staged;
  const checked = status?.checkedAt ? new Date(status.checkedAt).toLocaleString() : null;

  return (
    <div className="zk-update" data-testid="update-status">
      <dl className="zk-update__versions">
        <dt>{t('settings.update.current')}</dt>
        <dd data-testid="update-current">{status?.currentVersion || t('settings.update.unknown')}</dd>
        {status?.latestVersion ? (
          <>
            <dt>{t('settings.update.latest')}</dt>
            <dd data-testid="update-latest">
              {status.latestVersion}{' '}
              {status.available ? (
                <Tag color="processing">{t('settings.update.available')}</Tag>
              ) : (
                <Tag>{t('settings.update.current2')}</Tag>
              )}
            </dd>
          </>
        ) : null}
        {checked ? (
          <>
            <dt>{t('settings.update.checkedAt')}</dt>
            <dd>{checked}</dd>
          </>
        ) : null}
        {staged ? (
          <>
            <dt>{t('settings.update.staged')}</dt>
            <dd data-testid="update-staged">{staged.version}</dd>
          </>
        ) : null}
      </dl>

      {/* The check's own failure: a broken connection is not "up to date". */}
      {status?.error ? (
        <Alert
          type="warning"
          showIcon
          role="alert"
          title={t('settings.update.checkFailed')}
          description={status.error}
        />
      ) : null}
      {failure ? <Alert type="error" showIcon role="alert" title={failure} /> : null}

      <Space wrap>
        <Button
          loading={busy === 'check'}
          disabled={busy !== null && busy !== 'check'}
          onClick={() => void run('check', checkForUpdate)}
        >
          {t('settings.update.check')}
        </Button>
        {status?.available ? (
          <Button
            type="primary"
            loading={busy === 'download'}
            disabled={busy !== null && busy !== 'download'}
            onClick={() => void run('download', downloadUpdate)}
          >
            {t('settings.update.download')}
          </Button>
        ) : null}
        {staged ? (
          <Button
            loading={busy === 'install'}
            disabled={busy !== null && busy !== 'install'}
            onClick={() => void run('install', () => installUpdate(true))}
          >
            {t('settings.update.installNow')}
          </Button>
        ) : null}
      </Space>

      {staged ? (
        <Typography.Text type="secondary">{t('settings.update.installHint')}</Typography.Text>
      ) : null}

      {/* Release notes are Markdown from the release body. */}
      {status?.available && status.notes ? (
        <div className="zk-update__notes">
          <MarkdownBody text={status.notes} streaming={false} />
        </div>
      ) : null}
    </div>
  );
}