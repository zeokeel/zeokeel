import { Button, Typography } from 'antd';
import { useNavigate } from 'react-router';
import type { RouteMeta } from '@/features/routes';

/**
 * A registered-but-unbuilt route. It names the route reference and the reason,
 * rather than rendering a convincing-looking empty screen (no invented UI).
 */
export function StubScreen({ meta }: { meta: RouteMeta }) {
  const navigate = useNavigate();
  return (
    <div className="zk-stub">
      <Typography.Title level={4}>{meta.title}</Typography.Title>
      <Typography.Text className="zk-stub__meta">
        {meta.ref} · 本期未实现
      </Typography.Text>
      {meta.deferral ? <Typography.Text type="secondary">{meta.deferral}</Typography.Text> : null}
      <div>
        <Button onClick={() => navigate('/')}>回到 Now</Button>
      </div>
    </div>
  );
}

/** R25 · unknown route. Offers the one useful action: get back. */
export function NotFoundScreen() {
  const navigate = useNavigate();
  return (
    <div className="zk-stub">
      <Typography.Title level={4}>页面不存在</Typography.Title>
      <Typography.Text type="secondary">
        这个地址没有对应的屏面。
      </Typography.Text>
      <div>
        <Button type="primary" onClick={() => navigate('/')}>
          回到 Now
        </Button>
      </div>
    </div>
  );
}