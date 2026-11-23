import { useEffect, useState } from 'react';
import { Segmented, Spin, Table, Typography } from 'antd';
import { useSearchParams } from 'react-router';
import { loadInsights, type Insights } from '@/api/read-models';

/**
 * R24 · Insights — read-only usage. No chart library is added for this (the
 * antd-only stack has no charts), so the numbers are a summary row plus two
 * compact tables: honest, and it still answers "where did the tokens go".
 *
 * Cost is not shown at all: the gateway reports no price, and a `$0.00` would be
 * a fabricated value (S11).
 */
export function InsightsScreen() {
  const [searchParams, setSearchParams] = useSearchParams();
  const range = searchParams.get('range') === '30' ? 30 : 7;
  const [data, setData] = useState<Insights | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const value = await loadInsights(range);
        if (!cancelled) setData(value);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : '洞察加载失败');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [range]);

  return (
    <div className="zk-screen">
      <header className="zk-screen__header zk-screen__header--row">
        <div>
          <h1 className="zk-screen__title">洞察</h1>
          <p className="zk-screen__note">跨会话用量，只读。</p>
        </div>
        <Segmented
          size="small"
          value={String(range)}
          onChange={(value) =>
            setSearchParams(value === '7' ? {} : { range: String(value) }, { replace: true })
          }
          options={[
            { label: '近 7 天', value: '7' },
            { label: '近 30 天', value: '30' },
          ]}
        />
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
        <div className="zk-insights">
          <dl className="zk-insights__totals">
            {[
              ['会话', data.totals.sessions],
              ['轮次', data.totals.turns],
              ['输入 tokens', data.totals.inputTokens.toLocaleString()],
              ['输出 tokens', data.totals.outputTokens.toLocaleString()],
              ['工具调用', data.totals.toolCalls],
            ].map(([label, value]) => (
              <div key={String(label)} className="zk-insights__total">
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>

          <div className="zk-insights__tables">
            <section aria-label="按天">
              <Typography.Text strong>按天</Typography.Text>
              <Table
                size="small"
                rowKey="day"
                pagination={false}
                dataSource={data.byDay}
                columns={[
                  { title: '日期', dataIndex: 'day' },
                  { title: '轮次', dataIndex: 'turns', width: 100 },
                ]}
              />
            </section>
            <section aria-label="按工具">
              <Typography.Text strong>按工具</Typography.Text>
              <Table
                size="small"
                rowKey="tool"
                pagination={false}
                dataSource={data.byTool}
                columns={[
                  { title: '工具', dataIndex: 'tool' },
                  { title: '调用', dataIndex: 'calls', width: 100 },
                ]}
              />
            </section>
          </div>
        </div>
      )}
    </div>
  );
}