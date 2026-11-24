import { useCallback, useEffect, useState } from 'react';
import { Button, Empty, Form, Input, Modal, Space, Table, Tag, Typography } from 'antd';
import { TeamMemberDetail } from './member-detail';
import {
  closeTeammate,
  hasCapability,
  interruptTeammate,
  loadTeam,
  sendToTeammate,
  spawnTeammate,
  type TeamActivityRow,
  type TeamMember,
} from '@/api/gateway';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';

/**
 * R25 · 团队 — teammates and what they are doing.
 *
 * The gateway had no `team.*` method at all, so this screen could only show its
 * empty state. It now drives the real service: spawn a teammate, message it,
 * interrupt or close it, and read the activity feed. The service has no change
 * hook, so the snapshot is refreshed after every action and polled while the screen
 * is open (2s, the same ceiling the task board uses).
 */
export function TeamScreen() {
  const t = useT();
  const supported = hasCapability('team');
  const [members, setMembers] = useState<TeamMember[] | null>(null);
  const [activities, setActivities] = useState<TeamActivityRow[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [spawning, setSpawning] = useState(false);
  const [messaging, setMessaging] = useState<TeamMember | null>(null);
  const [detail, setDetail] = useState<TeamMember | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [spawnForm] = Form.useForm<{ name: string; prompt: string; model?: string }>();
  const [messageForm] = Form.useForm<{ message: string }>();

  const refresh = useCallback(async () => {
    if (!supported) return;
    try {
      const snapshot = await loadTeam();
      setMembers(snapshot.members);
      setActivities(snapshot.activities);
      setError(null);
    } catch (cause) {
      setError(describeError(cause, t, 'team.loadFailed'));
    }
  }, [supported, t]);

  useEffect(() => {
    if (!supported) return;
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
  }, [refresh, supported]);

  const spawn = async () => {
    const values = await spawnForm.validateFields();
    try {
      await spawnTeammate(values);
      setSpawning(false);
      spawnForm.resetFields();
      await refresh();
    } catch (cause) {
      setError(describeError(cause, t, 'team.spawnFailed'));
    }
  };

  const message = async (member: TeamMember) => {
    const values = await messageForm.validateFields();
    try {
      await sendToTeammate(member.id, values.message, { async: true });
      setMessaging(null);
      messageForm.resetFields();
      await refresh();
    } catch (cause) {
      setError(describeError(cause, t, 'team.sendFailed'));
    }
  };

  const act = async (member: TeamMember, kind: 'interrupt' | 'close') => {
    setBusy(member.id);
    try {
      if (kind === 'interrupt') await interruptTeammate(member.id);
      else await closeTeammate(member.id);
      await refresh();
    } catch (cause) {
      setError(describeError(cause, t, 'team.actionFailed'));
    } finally {
      setBusy(null);
    }
  };

  if (!supported) {
    return (
      <div className="zk-screen">
        <header className="zk-screen__header">
          <h1 className="zk-screen__title">{t('team.title')}</h1>
        </header>
        <Empty description={t('team.unsupported')} />
      </div>
    );
  }

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <div>
          <h1 className="zk-screen__title">{t('team.title')}</h1>
          <p className="zk-screen__note">{t('team.note')}</p>
        </div>
        <Button type="primary" onClick={() => setSpawning(true)} data-testid="team-spawn">
          {t('team.spawn')}
        </Button>
      </header>

      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : null}

      {members === null ? (
        <Empty description={t('team.loading')} />
      ) : members.length === 0 ? (
        <Empty description={t('team.empty.title')} />
      ) : (
        <Table
          size="small"
          rowKey="id"
          pagination={false}
          dataSource={members}
          columns={[
            { title: t('team.col.name'), dataIndex: 'name' },
            {
              title: t('team.col.state'),
              key: 'state',
              width: 140,
              render: (_: unknown, member: TeamMember) => (
                <Space size={4}>
                  <Tag color={member.runState === 'running' ? 'blue' : 'default'}>
                    {member.runState || member.status}
                  </Tag>
                  {member.error ? <Tag color="red">{t('team.col.failed')}</Tag> : null}
                </Space>
              ),
            },
            {
              title: t('team.col.model'),
              dataIndex: 'model',
              width: 200,
              render: (value: string | undefined) => value ?? '—',
            },
            {
              title: t('team.col.last'),
              dataIndex: 'last',
              render: (value: string | undefined) => (
                <Typography.Text type="secondary" ellipsis style={{ maxWidth: 320 }}>
                  {value || '—'}
                </Typography.Text>
              ),
            },
            {
              title: t('team.col.actions'),
              key: 'actions',
              width: 320,
              render: (_: unknown, member: TeamMember) => (
                <Space size={4}>
                  <Button size="small" onClick={() => setMessaging(member)}>
                    {t('team.message')}
                  </Button>
                  <Button size="small" onClick={() => setDetail(member)}>
                    {t('team.detail')}
                  </Button>
                  <Button
                    size="small"
                    loading={busy === member.id}
                    onClick={() => void act(member, 'interrupt')}
                  >
                    {t('team.interrupt')}
                  </Button>
                  <Button
                    size="small"
                    danger
                    loading={busy === member.id}
                    onClick={() => void act(member, 'close')}
                  >
                    {t('team.close')}
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      )}

      <div className="zk-team__activity">
        <Typography.Text strong>{t('team.activity')}</Typography.Text>
        <Table
          size="small"
          rowKey="id"
          pagination={false}
          dataSource={activities.slice(-30).reverse()}
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
              render: (_: unknown, row: TeamActivityRow) => row.text ?? row.memberId ?? '',
            },
          ]}
        />
      </div>

      <Modal
        open={detail !== null}
        title={t('team.detailTitle', { name: detail?.name ?? '' })}
        onCancel={() => setDetail(null)}
        footer={null}
        width={720}
      >
        {detail ? (
          <TeamMemberDetail memberId={detail.id} />
        ) : null}
      </Modal>

      <Modal
        open={spawning}
        title={t('team.spawnTitle')}
        onCancel={() => setSpawning(false)}
        onOk={() => void spawn()}
        okText={t('team.spawn')}
        cancelText={t('team.cancel')}
        destroyOnHidden
      >
        <Form form={spawnForm} layout="vertical" preserve={false}>
          <Form.Item name="name" label={t('team.field.name')} rules={[{ required: true, whitespace: true }]}>
            <Input />
          </Form.Item>
          <Form.Item
            name="prompt"
            label={t('team.field.prompt')}
            rules={[{ required: true, whitespace: true }]}
          >
            <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} />
          </Form.Item>
          <Form.Item name="model" label={t('team.field.model')}>
            <Input placeholder="qwen3.8-flash-next" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={messaging !== null}
        title={t('team.messageTitle', { name: messaging?.name ?? '' })}
        onCancel={() => setMessaging(null)}
        onOk={() => messaging && void message(messaging)}
        okText={t('team.send')}
        cancelText={t('team.cancel')}
        destroyOnHidden
      >
        <Form form={messageForm} layout="vertical" preserve={false}>
          <Form.Item
            name="message"
            label={t('team.field.message')}
            rules={[{ required: true, whitespace: true }]}
          >
            <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}