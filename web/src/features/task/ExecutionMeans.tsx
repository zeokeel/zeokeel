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
 * Copy comes from the catalogue except for three strings the catalogues do not
 * carry yet — see `capabilities-copy.ts` (TODO(i18n) there lists the keys).
 */
import { useEffect, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert, Button, Collapse, Empty, Skeleton, Space, Tag, Typography, Listy } from 'antd';
import {
  loadCapabilities,
  type Capabilities,
  type ConnectorSummary,
  type SkillSummary,
} from '@/api/read-models';
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

export function ExecutionMeans() {
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
            <Listy
              virtual={false}
              items={connectors}
              rowKey={(row, index) => `${index}:${row.name}`}
              itemRender={(row) => <ConnectorRow row={row} t={t} copy={copy} />}
            />
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
          children: body,
        },
      ]}
    />
  );
}