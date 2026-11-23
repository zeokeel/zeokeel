import { useEffect, useState } from 'react';
import { Empty, Spin, Table, Tabs, Tag, Typography } from 'antd';
import { useSearchParams } from 'react-router';
import { loadCapabilities, type Capabilities } from '@/api/read-models';
import { useT } from '@/i18n';

/** R23 · Capabilities — skills and MCP connectors, tab in the URL. */
export function CapabilitiesScreen() {
  const t = useT();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = searchParams.get('tab') === 'connectors' ? 'connectors' : 'skills';
  const [data, setData] = useState<Capabilities | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const value = await loadCapabilities();
        if (!cancelled) setData(value);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : t('screen.capabilities.loadFailed'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('screen.capabilities.title')}</h1>
        <p className="zk-screen__note">{t('screen.capabilities.note')}</p>
      </header>
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : data === null ? (
        <div className="zk-empty">
          <Spin />
        </div>
      ) : (
        <Tabs
          activeKey={tab}
          onChange={(key) => setSearchParams(key === 'skills' ? {} : { tab: key }, { replace: true })}
          items={[
            {
              key: 'skills',
              label: t('screen.capabilities.skills', { count: data.skills.length }),
              children:
                data.skills.length === 0 ? (
                  <Empty description={t('screen.capabilities.noSkills')} />
                ) : (
                  <Table
                    size="small"
                    rowKey="id"
                    pagination={false}
                    dataSource={data.skills}
                    columns={[
                      { title: t('screen.capabilities.col.name'), dataIndex: 'name', width: 160 },
                      { title: t('screen.capabilities.col.description'), dataIndex: 'description' },
                      {
                        title: t('screen.capabilities.col.source'),
                        dataIndex: 'source',
                        width: 120,
                        render: (value: string) => <Tag variant="filled">{value}</Tag>,
                      },
                    ]}
                  />
                ),
            },
            {
              key: 'connectors',
              label: t('screen.capabilities.connectors', { count: data.connectors.length }),
              children:
                data.connectors.length === 0 ? (
                  <Empty description={t('screen.capabilities.noConnectors')} />
                ) : (
                  <Table
                    size="small"
                    rowKey="id"
                    pagination={false}
                    dataSource={data.connectors}
                    columns={[
                      { title: t('screen.capabilities.col.name'), dataIndex: 'name' },
                      { title: t('screen.capabilities.col.transport'), dataIndex: 'transport' },
                      { title: t('screen.capabilities.col.tools'), dataIndex: 'tools', width: 100 },
                      {
                        title: t('screen.capabilities.col.status'),
                        dataIndex: 'status',
                        width: 140,
                        render: (value: string) => (
                          <Typography.Text type={value === 'connected' ? undefined : 'secondary'}>
                            {value === 'connected' ? t('screen.capabilities.connected') : t('screen.capabilities.idle')}
                          </Typography.Text>
                        ),
                      },
                    ]}
                  />
                ),
            },
          ]}
        />
      )}
    </div>
  );
}