import { useEffect, useState } from 'react';
import {
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import {
  createAutomation,
  deleteAutomation,
  loadAutomation,
  runAutomationNow,
  setAutomationEnabled,
  updateAutomation,
  type AutomationInput,
  type AutomationRun,
  type AutomationTask,
} from '@/api/read-models';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';

/**
 * R22 · Automation — scheduled session tasks.
 *
 * Native parity: create, edit, enable/disable, run now, delete, and the run history
 * each task carries. The screen had only the toggle because the gateway half did not
 * exist; it does now.
 */
export function AutomationScreen() {
  const t = useT();
  const [tasks, setTasks] = useState<AutomationTask[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<AutomationTask | null>(null);
  const [creating, setCreating] = useState(false);
  const [history, setHistory] = useState<AutomationTask | null>(null);
  const [form] = Form.useForm<AutomationInput>();

  // Initial load: the async IIFE keeps setState out of the effect body, which the
  // react-hooks rule requires (a synchronous setState there cascades renders).
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const value = await loadAutomation();
        if (!cancelled) setTasks(value);
      } catch (cause) {
        if (!cancelled) setError(describeError(cause, t, 'screen.automation.loadFailed'));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // After a write: refetch so next/last run and history reflect the gateway.
  const reload = async () => {
    try {
      setTasks(await loadAutomation());
      setError(null);
    } catch (cause) {
      setError(describeError(cause, t, 'screen.automation.loadFailed'));
    }
  };

  const toggle = async (task: AutomationTask, enabled: boolean) => {
    setTasks((current) =>
      (current ?? []).map((item) => (item.id === task.id ? { ...item, enabled } : item)),
    );
    try {
      await setAutomationEnabled(task.id, enabled);
    } catch (cause) {
      // Roll back: the switch must never claim a state the gateway rejected.
      setTasks((current) =>
        (current ?? []).map((item) => (item.id === task.id ? { ...item, enabled: !enabled } : item)),
      );
      setError(describeError(cause, t, 'screen.automation.toggleFailed'));
    }
  };

  const openCreate = () => {
    setEditing(null);
    form.setFieldsValue({ title: '', prompt: '', schedule: '', enabled: true });
    setCreating(true);
  };

  const openEdit = (task: AutomationTask) => {
    setEditing(task);
    form.setFieldsValue({
      title: task.title,
      prompt: task.prompt,
      schedule: task.schedule,
      enabled: task.enabled,
    });
    setCreating(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    try {
      if (editing) await updateAutomation(editing.id, values);
      else await createAutomation(values);
      setCreating(false);
      await reload();
    } catch (cause) {
      setError(describeError(cause, t, 'screen.automation.saveFailed'));
    }
  };

  const runNow = async (task: AutomationTask) => {
    try {
      await runAutomationNow(task.id);
      await reload();
    } catch (cause) {
      setError(describeError(cause, t, 'screen.automation.runFailed'));
    }
  };

  const remove = async (task: AutomationTask) => {
    try {
      await deleteAutomation(task.id);
      await reload();
    } catch (cause) {
      setError(describeError(cause, t, 'screen.automation.deleteFailed'));
    }
  };

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <div>
          <h1 className="zk-screen__title">{t('screen.automation.title')}</h1>
          <p className="zk-screen__note">{t('screen.automation.note')}</p>
        </div>
        <Button type="primary" onClick={openCreate} data-testid="automation-create">
          {t('screen.automation.create')}
        </Button>
      </header>
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : tasks === null ? (
        <div className="zk-empty">
          <Spin />
        </div>
      ) : tasks.length === 0 ? (
        <Empty description={t('screen.automation.empty')} />
      ) : (
        <Table
          size="small"
          rowKey="id"
          pagination={false}
          dataSource={tasks}
          columns={[
            {
              title: t('screen.automation.col.task'),
              dataIndex: 'title',
              render: (value: string, task: AutomationTask) => (
                <Space orientation="vertical" size={0}>
                  <Typography.Text strong>{value}</Typography.Text>
                  <Typography.Text type="secondary" ellipsis style={{ maxWidth: 420 }}>
                    {task.prompt}
                  </Typography.Text>
                  {task.lastError ? (
                    <Typography.Text type="danger">{task.lastError}</Typography.Text>
                  ) : null}
                </Space>
              ),
            },
            {
              title: t('screen.automation.col.schedule'),
              dataIndex: 'schedule',
              width: 170,
              render: (value: string) => <Typography.Text code>{value}</Typography.Text>,
            },
            {
              title: t('screen.automation.col.nextRun'),
              dataIndex: 'nextRun',
              width: 170,
              render: (value: string | undefined) => (value ? new Date(value).toLocaleString() : '—'),
            },
            {
              title: t('screen.automation.col.lastRun'),
              dataIndex: 'lastRun',
              width: 170,
              render: (value: string | undefined) => (value ? new Date(value).toLocaleString() : '—'),
            },
            {
              title: t('screen.automation.col.enabled'),
              key: 'enabled',
              width: 84,
              render: (_: unknown, task: AutomationTask) => (
                <Switch
                  size="small"
                  checked={task.enabled}
                  aria-label={t('screen.automation.enableOne', { title: task.title })}
                  onChange={(next) => void toggle(task, next)}
                />
              ),
            },
            {
              title: t('screen.automation.col.actions'),
              key: 'actions',
              width: 250,
              render: (_: unknown, task: AutomationTask) => (
                <Space size={4}>
                  <Button size="small" onClick={() => void runNow(task)}>
                    {t('screen.automation.runNow')}
                  </Button>
                  <Button size="small" onClick={() => openEdit(task)}>
                    {t('screen.automation.edit')}
                  </Button>
                  <Button size="small" onClick={() => setHistory(task)}>
                    {t('screen.automation.history')}
                  </Button>
                  <Popconfirm
                    title={t('screen.automation.deleteConfirm', { title: task.title })}
                    onConfirm={() => void remove(task)}
                  >
                    <Button size="small" danger>
                      {t('screen.automation.delete')}
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      )}

      <Modal
        open={creating}
        title={editing ? t('screen.automation.editTitle') : t('screen.automation.createTitle')}
        onCancel={() => setCreating(false)}
        onOk={() => void submit()}
        okText={t('screen.automation.save')}
        cancelText={t('screen.automation.cancel')}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="title"
            label={t('screen.automation.field.title')}
            rules={[{ required: true, whitespace: true }]}
          >
            <Input />
          </Form.Item>
          <Form.Item
            name="prompt"
            label={t('screen.automation.field.prompt')}
            rules={[{ required: true, whitespace: true }]}
          >
            <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} />
          </Form.Item>
          <Form.Item
            name="schedule"
            label={t('screen.automation.field.schedule')}
            extra={t('screen.automation.field.scheduleHelp')}
            rules={[{ required: true, whitespace: true }]}
          >
            <Input placeholder="0 8 * * *" />
          </Form.Item>
          <Form.Item name="enabled" label={t('screen.automation.field.enabled')} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={history !== null}
        title={t('screen.automation.historyTitle', { title: history?.title ?? '' })}
        onCancel={() => setHistory(null)}
        footer={null}
        width={720}
      >
        {history && history.runHistory.length > 0 ? (
          <ul className="zk-automation-history">
            {history.runHistory.map((run: AutomationRun, index: number) => (
              <li key={`${run.startedAt ?? ''}-${index}`} className="zk-automation-history__row">
                <Tag color={run.error ? 'red' : 'green'}>{run.status}</Tag>
                <Typography.Text type="secondary">
                  {run.startedAt ? new Date(run.startedAt).toLocaleString() : '—'}
                </Typography.Text>
                <Typography.Text>{run.error ? run.error : (run.summary ?? '')}</Typography.Text>
              </li>
            ))}
          </ul>
        ) : (
          <Empty description={t('screen.automation.historyEmpty')} />
        )}
      </Modal>
    </div>
  );
}