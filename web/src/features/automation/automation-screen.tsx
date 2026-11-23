import { useEffect, useState } from 'react';
import { Empty, Spin, Switch, Table, Typography } from 'antd';
import { loadAutomation, setAutomationEnabled, type AutomationTask } from '@/api/read-models';

/** R22 · Automation — scheduled tasks. Toggling is the only write in this milestone. */
export function AutomationScreen() {
  const [tasks, setTasks] = useState<AutomationTask[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const value = await loadAutomation();
        if (!cancelled) setTasks(value);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : '自动化加载失败');
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
      setError(cause instanceof Error ? cause.message : '切换失败');
    }
  };

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">自动化</h1>
        <p className="zk-screen__note">按时间表运行的会话。本轮只支持启停。</p>
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
        <Empty description="还没有定时任务。" />
      ) : (
        <>
          <Table
            size="small"
            rowKey="id"
            pagination={false}
            dataSource={tasks}
            columns={[
              { title: '任务', dataIndex: 'title' },
              {
                title: '时间表',
                dataIndex: 'schedule',
                width: 200,
                render: (value: string) => <Typography.Text code>{value}</Typography.Text>,
              },
              {
                title: '上次运行',
                dataIndex: 'lastRun',
                width: 180,
                render: (value: string | undefined) => value ?? '—',
              },
              {
                title: '启用',
                key: 'enabled',
                width: 96,
                render: (_: unknown, task: AutomationTask) => (
                  <Switch
                    size="small"
                    checked={task.enabled}
                    aria-label={`启用 ${task.title}`}
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