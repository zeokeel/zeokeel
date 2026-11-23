import { Button, Popover, Tag } from 'antd';
import { useEffect, useState } from 'react';
import { activeTaskCount, type GatewayTask } from '@/api/tasks';
import { deleteTask, setTaskStatus, subscribeTasks } from '@/api/gateway';
import { ZkIcon } from '@/design/icons';
import { useT } from '@/i18n';

/**
 * The background-task chip (B12).
 *
 * One chip, one popover: the board is written by agent tools and by other
 * clients, and the gateway only pushes its *own* writes — everything else is
 * polled with a 2 second ceiling (§5.1.8). So:
 *
 *  - the chip shows a count of unfinished work and nothing at all when the board
 *    is empty (never a `0`: an empty board is an empty surface, not a number);
 *  - 停止 is `task.status {status:"pending"}` — `pending` **is** the stop state;
 *  - 取消 is `task.delete`;
 *  - nothing here claims to be instant: the row updates when the delta lands.
 */
export function TaskChip({ sessionId }: { sessionId?: string }) {
  const t = useT();
  const [tasks, setTasks] = useState<GatewayTask[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    let unsubscribe: (() => void) | undefined;
    void subscribeTasks(sessionId, (items) => {
      if (!cancelled) setTasks(items);
    })
      .then((off) => {
        if (cancelled) off();
        else unsubscribe = off;
      })
      .catch(() => {
        // A board that cannot be read is not worth an error banner over the
        // composer; the chip simply stays absent.
        if (!cancelled) setError('unavailable');
      });
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [sessionId]);

  const active = activeTaskCount(tasks);
  if (error || tasks.length === 0) return null;

  const content = (
    <div className="zk-tasks">
      <ul className="zk-tasks__list">
        {tasks.map((task) => (
          <li key={task.id} className="zk-tasks__row" data-status={task.status}>
            <span className="zk-tasks__subject">{task.subject}</span>
            <Tag variant="filled">{statusLabel(task.status, t)}</Tag>
            {task.status === 'in_progress' ? (
              <Button
                size="small"
                type="text"
                onClick={() => void setTaskStatus(task.id, 'pending', sessionId)}
              >
                {t('task.stop')}
              </Button>
            ) : null}
            {task.status !== 'completed' ? (
              <Button
                size="small"
                type="text"
                onClick={() => void deleteTask(task.id, sessionId)}
              >
                {t('task.cancel')}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );

  return (
    <div className="zk-task-chip" role="group" aria-label={t('task.title')}>
      <Popover trigger="click" placement="topLeft" content={content} title={t('task.title')}>
        <Button size="small" type="text" icon={<ZkIcon name="clock" />}>
          {t('task.count', { count: active })}
        </Button>
      </Popover>
    </div>
  );
}

function statusLabel(status: GatewayTask['status'], t: ReturnType<typeof useT>): string {
  if (status === 'in_progress') return t('task.status.inProgress');
  if (status === 'completed') return t('task.status.completed');
  return t('task.status.pending');
}