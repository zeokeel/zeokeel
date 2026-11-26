/**
 * 预算 — what this task's ceiling is, how much of it is spent, and where to change
 * it (G11 + the 2026-11-26 audit, 高).
 *
 * A bound session reports real token usage and the task stops when it crosses its
 * ceiling. That stop is invisible unless the screen says so, so this block
 * answers three questions in the place they are asked: how much was spent, what
 * the limit was (`tasks.budget.get`), and — when the limit is reached — that the
 * task has stopped.
 *
 * It also owns the ONE write a reader needs here: 调整上限 (`tasks.budget.set`).
 * The earlier version of this file said topping up "lives on the surface that owns
 * that decision" — and no such surface existed: the method had zero callers in
 * `web/src`, while a dispatched task is created with a 200k/30min ceiling
 * automatically. A limit you can see and cannot change is a limit nobody set on
 * purpose. Two consequences shaped the entry:
 *  - it is two-step (保存, then an explicit 确认调整): raising a ceiling changes
 *    when the task stops, which is a decision, not a field edit;
 *  - an empty box is refused rather than read as 0, because `0` here means
 *    "unlimited" — the one value that must never be reached by accident.
 *
 * Reads and the write are separate states with separate failures: a budget read
 * that fails says so, in the gateway's own words, inside this block only, and the
 * task around it keeps working. "The budget did not load" and "there is no
 * ceiling" are different statements and this block never confuses them
 * (`{budget:null}` is an answer; a thrown error is not).
 *
 * The write's refusal is shown the same way: the gateway guards this call with a
 * permission action, so "没有权限" must arrive as the gateway's sentence, not as a
 * generic failure. The old ceiling stays on screen — nothing is claimed to have
 * changed that did not.
 *
 * What it deliberately does **not** do:
 *  - no 继续 button. Resuming a stopped task is the gate's job once the ceiling
 *    allows it (`SetBudget` parks the claim back through the ordinary gate); a
 *    button here would promise a resume this screen cannot perform.
 *  - no invented ceiling. `maxTokens`/`maxMinutes` of `0` mean unlimited, so the
 *    bar is only drawn when there is a limit to draw it against, and the copy
 *    says "no limit set" rather than "limit 0".
 *
 * The bar carries the semantic the rest of the product uses for these two states
 * instead of a colour of its own: antd's `normal` (in progress) and `exception`
 * (the error semantic, the same token an error uses elsewhere).
 *
 * Copy comes from the catalogue (`task.detail.budget.*`, and the existing
 * `task.detail.retry` / `task.detail.cancel`): this file carries no user-visible
 * string of its own.
 */
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Input, Progress, Skeleton, Space, Typography } from 'antd';
import { loadTaskBudget, setTaskBudget, type TaskBudget as Budget } from '@/api/task';
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

/** The write, as a state of its own: it must not overwrite the read's state. */
type SaveState = { kind: 'idle' } | { kind: 'saving' } | { kind: 'error'; message: string };

/** The two ceilings being edited, as the user typed them. */
interface Draft {
  tokens: string;
  minutes: string;
}

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

/**
 * One typed ceiling, or `null` when it is not a ceiling yet.
 *
 * Digits only: an empty box is "not decided", a sign or a decimal is a typo, and
 * neither may be rounded into a number the gateway would store. `0` is a real
 * value (unlimited), which is why the empty string is refused instead of being
 * treated as zero.
 */
function parseLimit(value: string): number | null {
  const trimmed = value.trim();
  if (!/^[0-9]+$/.test(trimmed)) return null;
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

/** The draft as the two numbers a write needs, or `null` while it is invalid. */
function limitsOf(draft: Draft): { maxTokens: number; maxMinutes: number } | null {
  const maxTokens = parseLimit(draft.tokens);
  const maxMinutes = parseLimit(draft.minutes);
  if (maxTokens === null || maxMinutes === null) return null;
  return { maxTokens, maxMinutes };
}

export function TaskBudget({ taskId }: { taskId: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<BudgetState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that reads.
  const [attempt, setAttempt] = useState(0);
  // `null` = the editor is closed. Opened from what the row actually says.
  const [draft, setDraft] = useState<Draft | null>(null);
  // The confirmation step: saving is deliberate, so it takes one more click.
  const [confirming, setConfirming] = useState(false);
  const [save, setSave] = useState<SaveState>({ kind: 'idle' });

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

  const budgetNow = state.kind === 'ready' ? state.budget : null;
  const limits = draft === null ? null : limitsOf(draft);

  const openEditor = () => {
    // Prefilled from the ceiling in force (`0` for "no ceiling"), so 保存 always
    // writes a decision the reader has seen.
    setDraft({
      tokens: String(budgetNow?.maxTokens ?? 0),
      minutes: String(budgetNow?.maxMinutes ?? 0),
    });
    setConfirming(false);
    setSave({ kind: 'idle' });
  };

  const closeEditor = () => {
    setDraft(null);
    setConfirming(false);
    setSave({ kind: 'idle' });
  };

  const commit = async (next: { maxTokens: number; maxMinutes: number }) => {
    setSave({ kind: 'saving' });
    try {
      const updated = await setTaskBudget({ id: taskId, ...next });
      // The gateway's answer is the state: usage kept, the stop re-derived from
      // usage versus the new limits. Nothing about it is recomputed here.
      setState({ kind: 'ready', budget: updated, at: Date.now() });
      setDraft(null);
      setConfirming(false);
      setSave({ kind: 'idle' });
    } catch (cause) {
      // A refused write is an answer: keep the old ceiling and say why.
      setSave({ kind: 'error', message: messageOf(cause) });
      setConfirming(false);
    }
  };

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

        {/* The stop, in one line: what happened and what unblocks it. */}
        {budget.exhausted ? (
          <Typography.Text type="danger" style={WRAP_LONG}>
            {t('task.detail.budget.exhausted')}
          </Typography.Text>
        ) : null}
      </Space>
    );
  }

  /** The write, offered only when the row (or its absence) is known. */
  let editor: ReactNode = null;
  if (state.kind === 'ready') {
    if (draft === null) {
      editor = (
        <Button size="small" data-testid="task-budget-adjust" onClick={openEditor}>
          {t('task.detail.budget.adjust')}
        </Button>
      );
    } else {
      editor = (
        <div data-testid="task-budget-editor">
          <Space orientation="vertical" size={4} style={{ width: '100%' }}>
            <Input
              aria-label={t('task.detail.budget.maxTokens')}
              value={draft.tokens}
              inputMode="numeric"
              onChange={(event) => setDraft({ ...draft, tokens: event.target.value })}
            />
            <Input
              aria-label={t('task.detail.budget.maxMinutes')}
              value={draft.minutes}
              inputMode="numeric"
              onChange={(event) => setDraft({ ...draft, minutes: event.target.value })}
            />
            <Typography.Text type="secondary">
              {t('task.detail.budget.zeroMeansUnlimited')}
            </Typography.Text>
            {limits === null ? (
              // Not a decoration: the save stays disabled, and this says why.
              <Typography.Text type="danger" data-testid="task-budget-invalid">
                {t('task.detail.budget.invalid')}
              </Typography.Text>
            ) : null}
            {save.kind === 'error' ? (
              <Alert
                type="error"
                showIcon
                role="alert"
                title={t('task.detail.budget.saveFailed')}
                description={save.message}
              />
            ) : null}
            {confirming ? (
              <Space wrap>
                <Typography.Text>{t('task.detail.budget.confirmTitle')}</Typography.Text>
                <Button
                  size="small"
                  type="primary"
                  loading={save.kind === 'saving'}
                  data-testid="task-budget-confirm"
                  onClick={() => {
                    if (limits !== null) void commit(limits);
                  }}
                >
                  {t('task.detail.budget.confirmAdjust')}
                </Button>
                <Button size="small" onClick={() => setConfirming(false)}>
                  {t('task.detail.cancel')}
                </Button>
              </Space>
            ) : (
              <Space wrap>
                <Button
                  size="small"
                  disabled={limits === null}
                  onClick={() => setConfirming(true)}
                >
                  {t('task.detail.budget.save')}
                </Button>
                <Button size="small" onClick={closeEditor}>
                  {t('task.detail.cancel')}
                </Button>
              </Space>
            )}
          </Space>
        </div>
      );
    }
  }

  return (
    // Its own testid so a test — and a reader — can say "the budget block"
    // without matching the 执行手段 rows next to it.
    <div data-testid="task-budget">
      <Typography.Text strong>{t('task.detail.budget.title')}</Typography.Text>
      {body}
      {editor}
    </div>
  );
}