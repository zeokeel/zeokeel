import { Button, Typography } from 'antd';
import { useNavigate } from 'react-router';
import { useT } from '@/i18n';

/**
 * R27 · unknown route. Offers the one useful action: get back.
 *
 * There is no placeholder screen any more (task-33): a route is either built or
 * not registered at all, because "registered but empty" tells the user a feature
 * exists when it does not. The gateway's missing capabilities are handled by
 * capability gating, which hides the entry instead of mocking one.
 */
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
