/**
 * 执行手段 — what this build can actually reach, shown on the task screen (M5).
 *
 * A person looking at a task asks two different questions: "how far has it
 * got?" (§2) and "can this team do it at all?". The answer to the second one is
 * not in the task row — it is the product's skills and MCP connectors, which
 * `GET /api/capabilities` owns (R23). This block puts that answer where the
 * question is asked, instead of behind a settings page the reader has to know
 * about.
 *
 * It is a read of its own, with its own failure: a capabilities read that fails
 * says so, in the gateway's own words, and the task around it stays exactly as
 * it was. "The list did not load" and "nothing is installed" are different
 * statements and this block never confuses them.
 *
 * Every row is informative or it is not rendered: a skill is its name plus the
 * line that says what it does; a connector is its name, the state the gateway
 * derived (`state`, never a comparison of localized sentences — round-2 C-04)
 * and, when it is broken, the provider's own error text. Connectors the toolkit
 * is skipping are a separate line, because "switched off" is not "not there".
 *
 * The block also carries the task's tool binding (M5 execution policy,
 * `tasks.tools.get/set`): "can this team do it?" has a per-task half — what this
 * one task is allowed to run. It is a read *and* a write of its own, so it keeps
 * its own state: a capabilities failure does not take the binding down, and a
 * failed rebind never leaves the boxes showing a policy the gateway rejected.
 *
 * Copy comes from the catalogue (`task.detail.means.*` and the binding's
 * `task.detail.means.binding.*`): this file carries no user-visible string of
 * its own.
 */
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  Empty,
  Skeleton,
  Space,
  Tag,
  Typography,
  Listy,
} from 'antd';
import {
  loadCapabilities,
  type Capabilities,
  type ConnectorSummary,
  type SkillSummary,
} from '@/api/read-models';
import { loadTaskTools, setTaskTools, type TaskTools } from '@/api/task';
import { useI18n, type MessageKey } from '@/i18n';

/**
 * A skill name, a connector name or a connector's error text is often one
 * unbroken token (a path, `dial tcp 127.0.0.1:9919`). `anywhere` keeps it inside
 * the column at 390px — same rule the screen applies to its own subjects.
 */
const WRAP_LONG: CSSProperties = { overflowWrap: 'anywhere' };

/** Skills beyond this are behind 展开全部: five is enough to recognise the set. */
const SKILL_LIMIT = 5;

/** One panel, so `defaultActiveKey` names it once. */
const PANEL_KEY = 'means';

type Translator = (key: MessageKey, params?: Record<string, string | number>) => string;

/** The read, as a state. Each value is the gateway's answer or the failure it produced. */
type MeansState =
  | { kind: 'loading' }
  | { kind: 'ready'; value: Capabilities }
  | { kind: 'error'; message: string };

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/** One skill: its name, and the one line that says what it does. */
function SkillRow({ row }: { row: SkillSummary }) {
  return (
    <Space size={4} wrap style={WRAP_LONG}>
      <Typography.Text strong style={WRAP_LONG}>
        {row.name}
      </Typography.Text>
      {row.description ? (
        <Typography.Text type="secondary" style={WRAP_LONG}>
          {row.description}
        </Typography.Text>
      ) : null}
    </Space>
  );
}

/**
 * One connector: name, the state the gateway derived, and — when it failed —
 * the reason in the provider's own words.
 *
 * `status` is only the fallback for a gateway that did not split the sentence
 * into `error` yet; a broken connector must never be able to hide its reason.
 */
function ConnectorRow({
  row,
  t,
  copy,
}: {
  row: ConnectorSummary;
  t: Translator;
  copy: { title: string; empty: string; unavailable: string };
}) {
  const state = row.state ?? 'idle';
  const tag =
    state === 'connected'
      ? { color: 'success' as const, label: t('screen.capabilities.state.connected') }
      : state === 'failed'
        ? { color: 'error' as const, label: copy.unavailable }
        : { color: 'default' as const, label: t('screen.capabilities.state.idle') };
  const reason = state === 'failed' ? row.error || row.status : '';
  return (
    <Space size={4} wrap style={WRAP_LONG}>
      <Typography.Text strong style={WRAP_LONG}>
        {row.name}
      </Typography.Text>
      <Tag color={tag.color}>{tag.label}</Tag>
      {row.transport ? <Typography.Text type="secondary">{row.transport}</Typography.Text> : null}
      {reason ? (
        <Typography.Text type="danger" style={WRAP_LONG}>
          {reason}
        </Typography.Text>
      ) : null}
    </Space>
  );
}

/**
 * The tool-name alphabet the gateway accepts (`tasklease/tools.go`:
 * `^[a-z0-9_.:-]{1,120}$`). A connector whose name falls outside it cannot be
 * stored as a binding, so it is not offered — a box that always fails the write
 * is worse than no box.
 */
const TOOL_NAME = /^[a-z0-9_.:-]{1,120}$/;

/** The binding read, as a state. `null` inside `ready` means "no binding". */
type BindingState =
  | { kind: 'loading' }
  | { kind: 'ready'; binding: TaskTools | null }
  | { kind: 'error'; message: string };

/** Same names, regardless of order: the gateway compares exact strings. */
function sameNames(left: readonly string[], right: readonly string[]): boolean {
  return left.length === right.length && left.every((name) => right.includes(name));
}

/**
 * One task's execution policy: which tools its owner may run.
 *
 * The boxes are the *current* binding, read back from the gateway
 * (`tasks.tools.get`); 保存 writes the whole list (a rebind replaces, it does
 * not merge, so a partial list would silently drop what is not in it) and 清除
 * writes `[]`, which the gateway stores as "no binding".
 *
 * Two rules the state machine keeps:
 *  - one write in flight at a time — every box and both buttons wait for it;
 *  - a failed write changes nothing, so the boxes return to the binding the
 *    gateway still holds instead of showing a policy it rejected.
 */
function ToolsBinding({
  taskId,
  connectors,
}: {
  taskId: string;
  connectors: ConnectorSummary[];
}) {
  const { t } = useI18n();
  // Copy from the catalogue (B5): the block carries no strings of its own.
  const bindingText = {
    title: t('task.detail.means.binding.title'),
    unbound: t('task.detail.means.binding.unbound'),
    clear: t('task.detail.means.binding.clear'),
    loadFailed: t('task.detail.means.binding.loadFailed'),
    connectorOnly: t('task.detail.means.binding.connectorOnly'),
  };
  const [state, setState] = useState<BindingState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that reads.
  const [attempt, setAttempt] = useState(0);
  /** The boxes, as edited. Only 保存/清除 move this to the gateway. */
  const [draft, setDraft] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  /** A failed write, shown next to the buttons without touching the binding. */
  const [writeError, setWriteError] = useState<string | null>(null);

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    loadTaskTools(taskId)
      .then((binding) => {
        if (cancelled) return;
        setState({ kind: 'ready', binding });
        setDraft(binding?.tools ?? []);
      })
      .catch((cause) => {
        if (!cancelled) setState({ kind: 'error', message: messageOf(cause) });
      });
    return () => {
      cancelled = true;
    };
  }, [taskId, attempt]);

  const bound = state.kind === 'ready' ? (state.binding?.tools ?? []) : [];
  // Connector names are the only tool names this build can derive (see the
  // connectorOnly line below). A name the task is already bound to is added even
  // when capabilities does not list it, so 保存 can never drop it unseen.
  const offered = connectors.map((row) => row.name).filter((name) => TOOL_NAME.test(name));
  const options = [...new Set([...offered, ...bound])];
  // The payload order is the order the boxes are shown: deterministic, and the
  // same list a reader can check by eye.
  const chosen = options.filter((name) => draft.includes(name));
  const dirty = !sameNames(chosen, bound);

  const write = async (tools: string[]) => {
    setBusy(true);
    setWriteError(null);
    const accepted = (binding: TaskTools): TaskTools | null =>
      binding.tools && binding.tools.length > 0 ? binding : null;
    try {
      const written = await setTaskTools({ id: taskId, tools });
      // The write answered: this is the policy the gateway accepted.
      setState({ kind: 'ready', binding: accepted(written) });
      setDraft(written.tools ?? []);
      // Then read it back, because the boxes promise to show the *stored*
      // policy rather than this component's optimism about it.
      let fresh = accepted(written);
      try {
        fresh = await loadTaskTools(taskId);
      } catch {
        // The write already answered; an unreadable echo changes none of it.
      }
      setState({ kind: 'ready', binding: fresh });
      setDraft(fresh?.tools ?? []);
    } catch (cause) {
      setWriteError(messageOf(cause));
      // Nothing changed on the gateway, so nothing changes in the boxes.
      setDraft(bound);
    } finally {
      setBusy(false);
    }
  };

  if (state.kind === 'loading') {
    return (
      <div data-testid="tools-binding">
        <Typography.Text strong>{bindingText.title}</Typography.Text>
        <Skeleton active title={false} paragraph={{ rows: 1 }} />
      </div>
    );
  }

  if (state.kind === 'error') {
    return (
      <div data-testid="tools-binding">
        <Typography.Text strong>{bindingText.title}</Typography.Text>
        <Alert
          type="error"
          showIcon
          role="alert"
          title={bindingText.loadFailed}
          description={state.message}
          action={
            <Button
              size="small"
              onClick={() => {
                setState({ kind: 'loading' });
                setAttempt((value) => value + 1);
              }}
            >
              {t('task.detail.retry')}
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <div data-testid="tools-binding">
      <Typography.Text strong>{bindingText.title}</Typography.Text>
      <Space orientation="vertical" size="small" style={{ width: '100%' }}>
        {/* "No binding" is an answer, and it is the one that matters most: it
            says the task is unrestricted rather than broken. */}
        {bound.length === 0 ? (
          <Typography.Text type="secondary">{bindingText.unbound}</Typography.Text>
        ) : null}

        {options.length > 0 ? (
          <Space orientation="vertical" size={4} style={{ width: '100%' }}>
            {options.map((name) => (
              <Checkbox
                key={name}
                checked={draft.includes(name)}
                disabled={busy}
                onChange={(event) =>
                  setDraft((prev) =>
                    event.target.checked
                      ? [...prev.filter((value) => value !== name), name]
                      : prev.filter((value) => value !== name),
                  )
                }
              >
                <span style={WRAP_LONG}>{name}</span>
              </Checkbox>
            ))}
          </Space>
        ) : null}

        {connectors.length > 0 ? (
          <Typography.Text type="secondary" style={WRAP_LONG}>
            {bindingText.connectorOnly}
          </Typography.Text>
        ) : null}

        {writeError ? (
          <Alert
            type="error"
            showIcon
            role="alert"
            closable
            title={writeError}
            onClose={() => setWriteError(null)}
          />
        ) : null}

        <Space wrap>
          <Button onClick={() => void write(chosen)} loading={busy} disabled={busy || !dirty}>
            {t('settings.save')}
          </Button>
          <Button onClick={() => void write([])} disabled={busy || bound.length === 0}>
            {bindingText.clear}
          </Button>
        </Space>
      </Space>
    </div>
  );
}

export function ExecutionMeans({ taskId }: { taskId: string }) {
  const { t } = useI18n();
  // Copy lives in the catalogue (B5): the block cannot carry its own strings.
  const copy = {
    title: t('task.detail.means.title'),
    empty: t('task.detail.means.empty'),
    unavailable: t('task.detail.means.unavailable'),
  };

  const [state, setState] = useState<MeansState>({ kind: 'loading' });
  // Bumped by 重试; the effect below is the only place that reads.
  const [attempt, setAttempt] = useState(0);
  const [allSkills, setAllSkills] = useState(false);

  useEffect(() => {
    let cancelled = false;
    // A read of its own: nothing here may make the task look unreadable.
    loadCapabilities()
      .then((value) => {
        if (!cancelled) setState({ kind: 'ready', value });
      })
      .catch((cause) => {
        if (!cancelled) setState({ kind: 'error', message: messageOf(cause) });
      });
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const retry = () => {
    // Back to the honest loading state, then read again.
    setState({ kind: 'loading' });
    setAttempt((value) => value + 1);
  };

  const skills = state.kind === 'ready' ? (state.value.skills ?? []) : [];
  const connectors = state.kind === 'ready' ? (state.value.connectors ?? []) : [];
  const disabled = state.kind === 'ready' ? (state.value.disabled ?? []) : [];
  const visibleSkills = allSkills ? skills : skills.slice(0, SKILL_LIMIT);
  // Nothing installed at all. Not "the lists are empty because the read failed":
  // that case is the alert below, and it never reaches this branch.
  const nothingInstalled =
    skills.length === 0 && connectors.length === 0 && disabled.length === 0;

  let body: ReactNode;
  if (state.kind === 'loading') {
    body = <Skeleton active title={false} paragraph={{ rows: 2 }} />;
  } else if (state.kind === 'error') {
    body = (
      // "The means did not load" is not "there are no means": the task stays on
      // screen and this says what actually happened, with a way to ask again.
      <Alert
        type="error"
        showIcon
        role="alert"
        title={t('screen.capabilities.loadFailed')}
        description={state.message}
        action={
          <Button size="small" onClick={retry}>
            {t('task.detail.retry')}
          </Button>
        }
      />
    );
  } else if (nothingInstalled) {
    body = <Empty description={copy.empty} image={Empty.PRESENTED_IMAGE_SIMPLE} />;
  } else {
    body = (
      <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
        <div>
          <Typography.Text strong>
            {t('screen.capabilities.skills', { count: skills.length })}
          </Typography.Text>
          {skills.length === 0 ? (
            <div>
              <Typography.Text type="secondary">{t('screen.capabilities.noSkills')}</Typography.Text>
            </div>
          ) : (
            <Listy
              virtual={false}
              items={visibleSkills}
              rowKey={(row, index) => `${index}:${row.name}`}
              itemRender={(row) => <SkillRow row={row} />}
            />
          )}
          {skills.length > SKILL_LIMIT ? (
            <Button size="small" onClick={() => setAllSkills((value) => !value)}>
              {allSkills ? t('task.detail.progress.less') : t('task.detail.progress.more')}
            </Button>
          ) : null}
        </div>

        <div>
          <Typography.Text strong>
            {t('screen.capabilities.connectors', { count: connectors.length })}
          </Typography.Text>
          {connectors.length === 0 ? (
            <div>
              <Typography.Text type="secondary">
                {t('screen.capabilities.noConnectors')}
              </Typography.Text>
            </div>
          ) : (
            // The connector rows answer "is it connected?"; the binding picker
            // below answers "is it allowed for this task?". Same names, two
            // different questions — hence the testid, so a test can say which.
            <div data-testid="means-connectors">
              <Listy
                virtual={false}
                items={connectors}
                rowKey={(row, index) => `${index}:${row.name}`}
                itemRender={(row) => <ConnectorRow row={row} t={t} copy={copy} />}
              />
            </div>
          )}
        </div>

        {/* Switched off is its own statement, not a missing row. */}
        {disabled.length > 0 ? (
          <div data-testid="means-disabled">
            <Space size={4} wrap style={WRAP_LONG}>
              <Typography.Text type="secondary">
                {t('screen.capabilities.state.disabled')}
              </Typography.Text>
              {disabled.map((name) => (
                <Tag key={name}>{name}</Tag>
              ))}
            </Space>
          </div>
        ) : null}
      </Space>
    );
  }

  // What the header says while the panel is closed: the counts, or the failure.
  // The block is collapsed-able but open by default, so the answer to "can this
  // team do it?" is on screen without a click.
  const summary =
    state.kind === 'ready'
      ? `${t('screen.capabilities.skills', { count: skills.length })} · ${t('screen.capabilities.connectors', { count: connectors.length })}`
      : state.kind === 'error'
        ? t('screen.capabilities.loadFailed')
        : null;

  return (
    <Collapse
      size="small"
      defaultActiveKey={[PANEL_KEY]}
      items={[
        {
          key: PANEL_KEY,
          label: <Typography.Text strong>{copy.title}</Typography.Text>,
          extra:
            summary === null ? null : (
              <Typography.Text type={state.kind === 'error' ? 'danger' : 'secondary'}>
                {summary}
              </Typography.Text>
            ),
          children: (
            <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
              {body}
              {/* Its own read and its own write: a capabilities failure above
                  does not take the binding down, and vice versa. */}
              <ToolsBinding taskId={taskId} connectors={connectors} />
            </Space>
          ),
        },
      ]}
    />
  );
}