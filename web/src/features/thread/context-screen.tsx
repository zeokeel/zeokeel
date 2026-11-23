import { Descriptions, Progress, Spin, Table, Tag, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router';
import { subscribeContext } from '@/api/gateway';
import { EMPTY_CONTEXT, windowUsage, type ContextState } from '@/api/context';
import { EmptyState } from '@/ui/empty-state';
import { useT } from '@/i18n';

/**
 * R6 · 上下文 — the folded context timeline (B6, docs/TRANSPORT.md §5.1.9).
 *
 * What it is *not*:
 *  - not live: the gateway re-folds every ~2s, so this is a stream of snapshots,
 *    and the copy never claims otherwise;
 *  - not a guess: `found:false` renders an empty state, because a cold session
 *    genuinely has no context to fold yet and printing `0` would be a number we
 *    did not measure;
 *  - not merged: each `context.delta` replaces the whole view.
 */
export function ContextScreen() {
  const t = useT();
  const { sessionId } = useParams<{ sessionId: string }>();
  const [state, setState] = useState<ContextState>(EMPTY_CONTEXT);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!sessionId) return;
    let cancelled = false;
    let unsubscribe: (() => void) | undefined;
    // `loading` starts true and is only cleared by the first snapshot: setting
    // it here would be a synchronous setState inside the effect (C4 rule).
    subscribeContext(sessionId, (next) => {
      if (cancelled) return;
      setState(next);
      setLoading(false);
    })
      .then((off) => {
        if (cancelled) off();
        else unsubscribe = off;
      })
      .catch(() => {
        if (!cancelled) {
          setError(true);
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [sessionId]);

  if (loading) {
    return (
      <div className="zk-context zk-context--center">
        <Spin />
      </div>
    );
  }

  if (error) {
    return (
      <div className="zk-context">
        <Typography.Text type="secondary">{t('context.loadFailed')}</Typography.Text>
      </div>
    );
  }

  if (!state.found || !state.context) {
    // Honest empty state: no zeros, no placeholder gauges.
    return (
      <div className="zk-context">
        <EmptyState title={t('context.empty.title')} description={t('context.empty.description')} />
      </div>
    );
  }

  const view = state.context;
  const used = windowUsage(view);

  return (
    <div className="zk-context">
      <Descriptions size="small" column={3} className="zk-context__summary">
        <Descriptions.Item label={t('context.model')}>{view.snapshot.model ?? '—'}</Descriptions.Item>
        <Descriptions.Item label={t('context.window')}>
          {view.window > 0 ? `${view.used.toLocaleString()} / ${view.window.toLocaleString()}` : '—'}
        </Descriptions.Item>
        <Descriptions.Item label={t('context.revision')}>#{view.revision}</Descriptions.Item>
      </Descriptions>

      {used === null ? null : (
        <Progress
          className="zk-context__usage"
          percent={Math.round(used * 100)}
          size="small"
          status={used > 0.9 ? 'exception' : 'normal'}
        />
      )}

      <Typography.Paragraph type="secondary" className="zk-context__note">
        {t('context.pollingNote')}
      </Typography.Paragraph>

      <section className="zk-context__sections">
        <Typography.Title level={5}>{t('context.sections')}</Typography.Title>
        <Table
          size="small"
          pagination={false}
          rowKey="id"
          dataSource={view.snapshot.sections ?? []}
          columns={[
            {
              title: t('context.section.id'),
              dataIndex: 'id',
              // The human label when the backend has one; the id otherwise.
              render: (_id, row: { id: string; label?: string }) => row.label ?? row.id,
            },
            { title: t('context.section.source'), dataIndex: 'source', render: (v) => v ?? '—' },
            { title: t('context.section.tokens'), dataIndex: 'tokens', align: 'right' },
            { title: t('context.section.bytes'), dataIndex: 'bytes', align: 'right' },
          ]}
        />
      </section>

      <section className="zk-context__events">
        <Typography.Title level={5}>{t('context.events')}</Typography.Title>
        <Table
          size="small"
          pagination={false}
          rowKey="seq"
          dataSource={view.events}
          columns={[
            { title: t('context.event.seq'), dataIndex: 'seq', width: 72 },
            { title: t('context.event.kind'), dataIndex: 'kind', width: 140 },
            { title: t('context.event.detail'), dataIndex: 'detail', render: (v) => v ?? '—' },
            {
              title: t('context.event.saved'),
              dataIndex: 'saved',
              align: 'right',
              render: (v) => (typeof v === 'number' && v > 0 ? v.toLocaleString() : '—'),
            },
          ]}
        />
      </section>

      <section className="zk-context__requests">
        <Typography.Title level={5}>{t('context.requests')}</Typography.Title>
        <Table
          size="small"
          pagination={false}
          rowKey="seq"
          dataSource={view.requests}
          columns={[
            { title: t('context.request.iteration'), dataIndex: 'iteration', width: 90 },
            { title: t('context.request.prompt'), dataIndex: 'prompt', align: 'right' },
            { title: t('context.request.completion'), dataIndex: 'completion', align: 'right' },
            { title: t('context.request.contextTokens'), dataIndex: 'contextTokens', align: 'right' },
            {
              title: t('context.request.model'),
              dataIndex: 'model',
              render: (v) => v ?? '—',
            },
          ]}
        />
      </section>

      {view.costKnown && typeof view.totalCostUsd === 'number' ? (
        <Tag className="zk-context__cost">
          {t('context.totalCost', { cost: view.totalCostUsd.toFixed(4) })}
        </Tag>
      ) : null}
    </div>
  );
}