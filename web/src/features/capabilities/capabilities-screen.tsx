import { useEffect, useState } from 'react';
import { Empty, Spin, Table, Tabs, Tag, Typography } from 'antd';
import { useSearchParams } from 'react-router';
import { loadCapabilities, type Capabilities } from '@/api/read-models';

/** R23 · Capabilities — skills and MCP connectors, tab in the URL. */
export function CapabilitiesScreen() {
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
        if (!cancelled) setError(cause instanceof Error ? cause.message : '能力加载失败');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">能力</h1>
        <p className="zk-screen__note">装了什么技能、连了哪些 MCP 服务器。</p>
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
              label: `技能 ${data.skills.length}`,
              children:
                data.skills.length === 0 ? (
                  <Empty description="没有加载技能。" />
                ) : (
                  <Table
                    size="small"
                    rowKey="id"
                    pagination={false}
                    dataSource={data.skills}
                    columns={[
                      { title: '名称', dataIndex: 'name', width: 160 },
                      { title: '说明', dataIndex: 'description' },
                      {
                        title: '来源',
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
              label: `连接器 ${data.connectors.length}`,
              children:
                data.connectors.length === 0 ? (
                  <Empty description="没有连接器。" />
                ) : (
                  <Table
                    size="small"
                    rowKey="id"
                    pagination={false}
                    dataSource={data.connectors}
                    columns={[
                      { title: '名称', dataIndex: 'name' },
                      { title: '传输', dataIndex: 'transport' },
                      { title: '工具数', dataIndex: 'tools', width: 100 },
                      {
                        title: '状态',
                        dataIndex: 'status',
                        width: 140,
                        render: (value: string) => (
                          <Typography.Text type={value === 'connected' ? undefined : 'secondary'}>
                            {value === 'connected' ? '已连接' : '空闲'}
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