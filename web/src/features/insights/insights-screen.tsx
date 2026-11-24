import { useEffect, useState } from 'react';
import { Segmented, Spin, Table, Typography } from 'antd';
import { useSearchParams } from 'react-router';
import { loadInsights, type Insights } from '@/api/read-models';
import { useT } from '@/i18n';

/**
 * R24 · Insights — read-only usage. No chart library is added for this (the
 * antd-only stack has no charts), so the numbers are a summary row plus two
 * compact tables: honest, and it still answers "where did the tokens go".
 *
 * Cost is not shown at all: the gateway reports no price, and a `$0.00` would be
 * a fabricated value (S11).
 */
export function InsightsScreen() {
  const t = useT();
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
        if (!cancelled) setError(cause instanceof Error ? cause.message : t('screen.insights.loadFailed'));
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
          <h1 className="zk-screen__title">{t('screen.insights.title')}</h1>
          <p className="zk-screen__note">{t('screen.insights.note')}</p>
        </div>
        <Segmented
          size="small"
          value={String(range)}
          onChange={(value) =>
            setSearchParams(value === '7' ? {} : { range: String(value) }, { replace: true })
          }
          options={[
            { label: t('screen.insights.range.last7'), value: '7' },
            { label: t('screen.insights.range.last30'), value: '30' },
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
              [t('screen.insights.sessions'), data.totals.sessions],
              [t('screen.insights.turns'), data.totals.turns],
              [t('screen.insights.inputTokens'), data.totals.inputTokens.toLocaleString()],
              [t('screen.insights.outputTokens'), data.totals.outputTokens.toLocaleString()],
              [t('screen.insights.toolCalls'), data.totals.toolCalls],
            ].map(([label, value]) => (
              <div key={String(label)} className="zk-insights__total">
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>

          <div className="zk-insights__tables">
            <section aria-label={t('screen.insights.byDay')}>
              <Typography.Text strong>{t('screen.insights.byDay')}</Typography.Text>
              <Table
                size="small"
                rowKey="day"
                pagination={false}
                dataSource={data.byDay}
                columns={[
                  { title: t('screen.insights.col.day'), dataIndex: 'day' },
                  { title: t('screen.insights.col.turns'), dataIndex: 'turns', width: 100 },
                ]}
              />
            </section>
            <section aria-label={t('screen.insights.byModel')}>
              <Typography.Text strong>{t('screen.insights.byModel')}</Typography.Text>
              <Table
                size="small"
                rowKey="model"
                pagination={false}
                dataSource={data.byModel}
                columns={[
                  { title: t('screen.insights.col.model'), dataIndex: 'model' },
                  { title: t('screen.insights.col.calls'), dataIndex: 'calls', width: 100 },
                  { title: t('screen.insights.col.tokens'), dataIndex: 'totalTokens', width: 120 },
                ]}
              />
            </section>
          </div>
        </div>
      )}
    </div>
  );
}