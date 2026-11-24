import { useCallback, useEffect, useState } from 'react';
import { Button, Descriptions, Empty, Form, Input, Space, Table, Tag, Typography } from 'antd';
import {
  closeTeammate,
  interruptTeammate,
  loadTeam,
  sendToTeammate,
  type TeamActivityRow,
  type TeamMember,
} from '@/api/gateway';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';

/**
 * One teammate's detail: who it is, what it last answered, the activity rows that
 * belong to it, and a box to message it — the native client's member page.
 *
 * Shared by the team screen (modal) and the `/c/:sessionId/child/:childId` route, so
 * the two cannot drift.
 */
export function TeamMemberDetail({ memberId }: { memberId: string }) {
  const t = useT();
  const [member, setMember] = useState<TeamMember | null>(null);
  const [activity, setActivity] = useState<TeamActivityRow[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm<{ message: string }>();

  const refresh = useCallback(async () => {
    try {
      const snapshot = await loadTeam();
      const found = snapshot.members.find((item) => item.id === memberId) ?? null;
      setMember(found);
      setActivity(snapshot.activities.filter((row) => row.memberId === memberId));
      setError(null);
    } catch (cause) {
      setError(describeError(cause, t, 'team.loadFailed'));
    }
  }, [memberId, t]);

  useEffect(() => {
    let cancelled = false;
    const tick = async () => {
      if (cancelled) return;
      await refresh();
    };
    void tick();
    const timer = setInterval(() => void tick(), 2000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [refresh]);

  const send = async () => {
    const values = await form.validateFields();
    try {
      await sendToTeammate(memberId, values.message, { async: true });
      form.resetFields();
      await refresh();
    } catch (cause) {
      setError(describeError(cause, t, 'team.sendFailed'));
    }
  };

  const act = async (kind: 'interrupt' | 'close') => {
    setBusy(true);
    try {
      if (kind === 'interrupt') await interruptTeammate(memberId);
      else await closeTeammate(memberId);
      await refresh();
    } catch (cause) {
      setError(describeError(cause, t, 'team.actionFailed'));
    } finally {
      setBusy(false);
    }
  };

  if (error) {
    return (
      <div className="zk-msg-error" role="alert">
        {error}
      </div>
    );
  }
  if (!member) {
    return <Empty description={t('team.memberMissing')} />;
  }

  return (
    <div className="zk-member-detail">
      <Space size={8} wrap>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {member.name}
        </Typography.Title>
        <Tag color={member.runState === 'running' ? 'blue' : 'default'}>
          {member.runState || member.status}
        </Tag>
        {member.error ? <Tag color="red">{t('team.col.failed')}</Tag> : null}
      </Space>

      <Descriptions size="small" column={1} style={{ marginTop: 12 }}>
        <Descriptions.Item label={t('team.col.model')}>
          {member.model || t('team.inheritModel')}
        </Descriptions.Item>
        <Descriptions.Item label={t('team.detail.created')}>
          {member.created ? new Date(member.created).toLocaleString() : '—'}
        </Descriptions.Item>
        <Descriptions.Item label={t('team.detail.lastAnswer')}>
          {member.last || '—'}
        </Descriptions.Item>
        {member.error ? (
          <Descriptions.Item label={t('team.detail.error')}>
            <Typography.Text type="danger">{member.error}</Typography.Text>
          </Descriptions.Item>
        ) : null}
      </Descriptions>

      <Typography.Text strong>{t('team.activity')}</Typography.Text>
      <Table
        size="small"
        rowKey="id"
        pagination={false}
        dataSource={activity.slice(-20).reverse()}
        locale={{ emptyText: t('team.activityEmpty') }}
        columns={[
          {
            title: t('team.activityTime'),
            dataIndex: 'at',
            width: 110,
            render: (value: string | undefined) =>
              value ? new Date(value).toLocaleTimeString() : '—',
          },
          {
            title: t('team.activityKind'),
            dataIndex: 'kind',
            width: 120,
            render: (value: string) => <Tag>{value}</Tag>,
          },
          {
            title: t('team.activityText'),
            key: 'text',
            render: (_: unknown, row: TeamActivityRow) => row.text ?? '',
          },
        ]}
      />

      <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
        <Form.Item
          name="message"
          label={t('team.field.message')}
          rules={[{ required: true, whitespace: true }]}
        >
          <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
        </Form.Item>
        <Space>
          <Button type="primary" onClick={() => void send()}>
            {t('team.send')}
          </Button>
          <Button loading={busy} onClick={() => void act('interrupt')}>
            {t('team.interrupt')}
          </Button>
          <Button danger loading={busy} onClick={() => void act('close')}>
            {t('team.close')}
          </Button>
        </Space>
      </Form>
    </div>
  );
}