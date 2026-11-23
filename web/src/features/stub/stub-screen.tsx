import { Button, Typography } from 'antd';
import { useNavigate } from 'react-router';
import type { RouteMeta } from '@/features/routes';
import { useT } from '@/i18n';

/**
 * A registered-but-unbuilt route. It names the route reference and the reason,
 * rather than rendering a convincing-looking empty screen (no invented UI).
 *
 * Only R6 上下文 reaches this screen today: the gateway has no `context.*`
 * method, and `deferral` is the field that says so.
 */
export function StubScreen({ meta }: { meta: RouteMeta }) {
  const navigate = useNavigate();
  const t = useT();
  return (
    <div className="zk-stub">
      <Typography.Title level={4}>{t(meta.titleKey)}</Typography.Title>
      <Typography.Text className="zk-stub__meta">{meta.ref}</Typography.Text>
      {meta.deferral ? <Typography.Text type="secondary">{meta.deferral}</Typography.Text> : null}
      <div>
        <Button onClick={() => navigate('/')}>{t('notFound.back')}</Button>
      </div>
    </div>
  );
}

/** R27 · unknown route. Offers the one useful action: get back. */
export function NotFoundScreen() {
  const navigate = useNavigate();
  const t = useT();
  return (
    <div className="zk-stub">
      <Typography.Title level={4}>{t('notFound.title')}</Typography.Title>
      <Typography.Text type="secondary">{t('notFound.description')}</Typography.Text>
      <div>
        <Button type="primary" onClick={() => navigate('/')}>
          {t('notFound.back')}
        </Button>
      </div>
    </div>
  );
}
