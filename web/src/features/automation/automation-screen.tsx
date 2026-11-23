import { useEffect, useState } from 'react';
import { Empty, Spin, Switch, Table, Typography } from 'antd';
import { loadAutomation, setAutomationEnabled, type AutomationTask } from '@/api/read-models';
import { useT } from '@/i18n';
import { describeError } from '@/features/describe-error';

/** R22 · Automation — scheduled tasks. Toggling is the only write in this milestone. */
export function AutomationScreen() {
  const t = useT();
  const [tasks, setTasks] = useState<AutomationTask[] | null>(null);
  const [error, setError] = useState<string | null>(null);

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

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('screen.automation.title')}</h1>
        <p className="zk-screen__note">{t('screen.automation.note')}</p>
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
        <>
          <Table
            size="small"
            rowKey="id"
            pagination={false}
            dataSource={tasks}
            columns={[
              { title: t('screen.automation.col.task'), dataIndex: 'title' },
              {
                title: t('screen.automation.col.schedule'),
                dataIndex: 'schedule',
                width: 200,
                render: (value: string) => <Typography.Text code>{value}</Typography.Text>,
              },
              {
                title: t('screen.automation.col.lastRun'),
                dataIndex: 'lastRun',
                width: 180,
                render: (value: string | undefined) => value ?? '—',
              },
              {
                title: t('screen.automation.col.enabled'),
                key: 'enabled',
                width: 96,
                render: (_: unknown, task: AutomationTask) => (
                  <Switch
                    size="small"
                    checked={task.enabled}
                    aria-label={t('screen.automation.enableOne', { title: task.title })}
                    onChange={(next) => void toggle(task, next)}
                  />
                ),
              },
            ]}
          />
        </>
      )}
    </div>
  );
}