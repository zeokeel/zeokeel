/**
 * 预算 — how much of this task's ceiling is spent, on the task screen (G11).
 *
 * A bound session reports real token usage and the task stops when it crosses
 * its ceiling. That stop is invisible unless the screen says so, so this block
 * answers three questions in the place they are asked: how much was spent, what
 * the limit was (`tasks.budget.get`), and — when the limit is reached — that the
 * task has stopped.
 *
 * It is a read of its own, with its own failure: a budget read that fails says
 * so, in the gateway's own words, inside this block only, and the task around it
 * keeps working. "The budget did not load" and "there is no ceiling" are
 * different statements and this block never confuses them (`{budget:null}` is an
 * answer; a thrown error is not).
 *
 * Two things it deliberately does **not** do:
 *  - no 继续 button. Topping up is a Lead action through `tasks.budget.set`, and
 *    it lives on the surface that owns that decision; a button here would put a
 *    write behind a screen that is read-only about budgets.
 *  - no invented ceiling. `maxTokens`/`maxMinutes` of `0` mean unlimited, so the
 *    bar is only drawn when there is a limit to draw it against, and the copy
 *    says "no limit set" rather than "limit 0".
 *
 * The bar carries the semantic the rest of the product uses for these two states
 * instead of a colour of its own: antd's `normal` (in progress) and `exception`
 * (the error semantic, the same token an error uses elsewhere).
 *
 * Copy comes from the catalogue (`task.detail.budget.*`, and the existing
 * `task.detail.retry`): this file carries no user-visible string of its own.
 */
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Progress, Skeleton, Space, Typography } from 'antd';
import { loadTaskBudget, type TaskBudget as Budget } from '@/api/task';
import { useI18n } from '@/i18n';

/**
 * A budget line is short, but the reason a read failed is often one unbroken
 * token (`dial tcp 127.0.0.1:9919`). Same wrapping rule as the screen's own.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** The read, as a state. `null` inside `ready` means "no ceiling". */
type BudgetState =
  | { kind: 'loading' }
  | {
      kind: 'ready';
      budget: Budget | null;
      /**
       * The clock the minutes are measured against, captured with the read — the
       * same rule the screen applies to its relative stamps. A `Date.now()` in
       * render would be an impure read of the wall clock on every pass.
       */
      at: number;
    }
  | { kind: 'error'; message: string };

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/** Minutes spent since the budget's clock started, or `null` when it has not. */
function minutesSpent(budget: Budget, now: number): number | null {
  if (!budget.startedAt) return null;
  const started = Date.parse(budget.startedAt);
  if (Number.isNaN(started)) return null;
  return Math.max(0, Math.floor((now - started) / 60_000));
}

export function TaskBudget({ taskId }: { taskId: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<BudgetState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that reads.
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    // A read of its own: nothing here may make the task look unreadable.
    loadTaskBudget(taskId)
      .then((budget) => {
        if (!cancelled) setState({ kind: 'ready', budget, at: Date.now() });
      })
      .catch((cause) => {
        if (!cancelled) setState({ kind: 'error', message: messageOf(cause) });
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, attempt]);

  let body: ReactNode;
  if (state.kind === 'loading') {
    body = <Skeleton active title={false} paragraph={{ rows: 1 }} />;
  } else if (state.kind === 'error') {
    body = (
      // "The budget did not load" is not "there is no ceiling": the failure is
      // reported here and the rest of the screen keeps its own answer.
      <Alert
        type="error"
        showIcon
        role="alert"
        title={t('task.detail.budget.loadFailed')}
        description={state.message}
        action={
          <Button
            size="small"
            onClick={() => {
              // Back to the honest loading state, then read again — this read
              // alone; the task's own reads are not repeated.
              setState({ kind: 'loading' });
              setAttempt((value) => value + 1);
            }}
          >
            {t('task.detail.retry')}
          </Button>
        }
      />
    );
  } else if (!state.budget) {
    // An answer, not a failure: this task was dispatched with no ceiling.
    body = (
      <Typography.Text type="secondary">{t('task.detail.budget.none')}</Typography.Text>
    );
  } else {
    const budget = state.budget;
    const spent = minutesSpent(budget, state.at);
    // A limited ceiling is the only case with a meaningful percentage; an
    // exhausted budget is 100% by definition, even when the stop was the clock.
    const percent = budget.exhausted
      ? 100
      : budget.maxTokens > 0
        ? Math.min(100, Math.round((budget.usedTokens / budget.maxTokens) * 100))
        : 0;
    body = (
      <Space orientation="vertical" size={4} style={{ width: '100%' }}>
        {budget.maxTokens > 0 ? (
          <span style={WRAP_LONG}>
            {t('task.detail.budget.used', {
              used: budget.usedTokens.toLocaleString(),
              limit: budget.maxTokens.toLocaleString(),
            })}
          </span>
        ) : (
          // 0 = unlimited. Printing "limit 0 tokens" would be a ceiling the
          // gateway never set.
          <span style={WRAP_LONG}>
            {t('task.detail.budget.usedUnlimited', { used: budget.usedTokens.toLocaleString() })}
          </span>
        )}

        {budget.maxMinutes > 0 ? (
          <Typography.Text type="secondary" style={WRAP_LONG}>
            {spent === null
              ? t('task.detail.budget.minutesLimitOnly', { limit: budget.maxMinutes })
              : t('task.detail.budget.minutes', { used: spent, limit: budget.maxMinutes })}
          </Typography.Text>
        ) : null}

        {budget.maxTokens > 0 ? (
          <Progress
            percent={percent}
            size="small"
            // In progress vs the error semantic — the same two states the rest
            // of the product draws with these tokens.
            status={budget.exhausted ? 'exception' : 'normal'}
          />
        ) : null}

        {/* The stop, in one line: what happened and what unblocks it. The
            unblocking itself is a Lead action on another surface. */}
        {budget.exhausted ? (
          <Typography.Text type="danger" style={WRAP_LONG}>
            {t('task.detail.budget.exhausted')}
          </Typography.Text>
        ) : null}
      </Space>
    );
  }

  return (
    // Its own testid so a test — and a reader — can say "the budget block"
    // without matching the 执行手段 rows next to it.
    <div data-testid="task-budget">
      <Typography.Text strong>{t('task.detail.budget.title')}</Typography.Text>
      {body}
    </div>
  );
}