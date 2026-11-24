import { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Spin,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { useSearchParams } from 'react-router';
import { loadCapabilities, type Capabilities, type ConnectorSummary } from '@/api/read-models';
import {
  reloadConnectors,
  setSetting,
  subscribeSettings,
  type SettingsRows,
} from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';
import { describeError } from '@/features/describe-error';
import { useT, type MessageKey } from '@/i18n';

/**
 * R23 · Capabilities — skills and MCP connectors, tab in the URL.
 *
 * The connectors tab used to be a read-only mirror of whatever the toolkit had
 * connected at activation, while the only way to add a server was a hand-written
 * JSON array in 设置 → 数据与隐私 (round-2 C-01). This page now owns the whole
 * loop: add / edit / enable / delete, then `mcp.reload` so the change is real
 * without restarting the gateway (C-03).
 */

/** One `mcp.servers` row. Unknown fields are preserved on save. */
type ConnectorRow = Record<string, unknown> & {
  name: string;
  transport?: string;
  url?: string;
  command?: string;
  args?: string[];
  env?: string[];
  timeoutMs?: number;
  workspaceId?: string;
};

export function parseConnectors(raw: string): ConnectorRow[] {
  if (!raw.trim()) return [];
  try {
    const decoded = JSON.parse(raw);
    if (!Array.isArray(decoded)) return [];
    return decoded.filter((row): row is ConnectorRow => Boolean(row) && typeof row === 'object');
  } catch {
    return [];
  }
}

/** The state a row is rendered from; the gateway derives it, we do not guess. */
function connectorState(connector: ConnectorSummary | undefined, disabled: boolean): {
  tone: 'success' | 'error' | 'neutral' | 'warning';
  labelKey: MessageKey;
} {
  if (disabled) return { tone: 'neutral', labelKey: 'screen.capabilities.state.disabled' };
  switch (connector?.state) {
    case 'connected':
      return { tone: 'success', labelKey: 'screen.capabilities.state.connected' };
    case 'failed':
      return { tone: 'error', labelKey: 'screen.capabilities.state.failed' };
    default:
      return { tone: 'warning', labelKey: 'screen.capabilities.state.idle' };
  }
}

export function CapabilitiesScreen() {
  const t = useT();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = searchParams.get('tab') === 'connectors' ? 'connectors' : 'skills';
  const [data, setData] = useState<Capabilities | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [rows, setRows] = useState<SettingsRows>({});
  const [editing, setEditing] = useState<ConnectorRow | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [form] = Form.useForm<ConnectorRow & { argsText?: string; envText?: string }>();

  useEffect(() => subscribeSettings(setRows), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const value = await loadCapabilities();
        if (!cancelled) setData(value);
      } catch (cause) {
        if (!cancelled) {
          setError(cause instanceof Error ? cause.message : t('screen.capabilities.loadFailed'));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  const configured = useMemo(
    () => parseConnectors(String(rows[SETTING_KEYS.mcpServers] ?? '')),
    [rows],
  );
  // The gateway owns the key (`mcp.disabledServers.<workspaceId>`) because the
  // browser does not know the workspace id; writing the bare key from here made
  // the switch a no-op (round-3 conn audit F1).
  const disabledNames = useMemo(() => data?.disabled ?? [], [data]);
  const live = useMemo(() => {
    const map = new Map<string, ConnectorSummary>();
    for (const connector of data?.connectors ?? []) map.set(connector.name, connector);
    return map;
  }, [data]);

  const persist = async (next: ConnectorRow[], disabled?: string[]) => {
    setBusy(true);
    setError(null);
    try {
      await setSetting(SETTING_KEYS.mcpServers, JSON.stringify(next));
      // The write is only half the change: the toolkit connects on reload, and
      // the disabled list travels with the reload so the gateway writes it under
      // the key it actually reads.
      const result = await reloadConnectors(disabled);
      setData((current) => ({
        skills: current?.skills ?? data?.skills ?? [],
        connectors: result.connectors,
        disabled: result.disabled,
      }));
      setNotice(t('screen.capabilities.saved'));
    } catch (cause) {
      setError(describeError(cause, t, 'screen.capabilities.saveFailed'));
    } finally {
      setBusy(false);
    }
  };

  const refresh = async () => {
    setBusy(true);
    setError(null);
    try {
      const result = await reloadConnectors();
      setData((current) => ({
        skills: current?.skills ?? [],
        connectors: result.connectors,
        disabled: result.disabled,
      }));
    } catch (cause) {
      setError(describeError(cause, t, 'screen.capabilities.saveFailed'));
    } finally {
      setBusy(false);
    }
  };

  const openAdd = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ transport: 'http', name: '', url: '', timeoutMs: 0 });
    setFormOpen(true);
  };

  const openEdit = (row: ConnectorRow) => {
    setEditing(row);
    form.setFieldsValue({
      ...row,
      argsText: (row.args ?? []).join('\n'),
      envText: (row.env ?? []).join('\n'),
    });
    setFormOpen(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    const transport = values.transport === 'stdio' ? 'stdio' : 'http';
    const row: ConnectorRow = { ...(editing ?? ({ name: '' } as ConnectorRow)) };
    row.name = String(values.name ?? '').trim();
    row.transport = transport;
    if (transport === 'http') {
      row.url = String(values.url ?? '').trim();
      delete row.command;
      delete row.args;
      delete row.env;
    } else {
      row.command = String(values.command ?? '').trim();
      const args = String(values.argsText ?? '')
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean);
      const env = String(values.envText ?? '')
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean);
      if (args.length > 0) row.args = args;
      else delete row.args;
      if (env.length > 0) row.env = env;
      else delete row.env;
      delete row.url;
    }
    if (values.timeoutMs && values.timeoutMs > 0) row.timeoutMs = values.timeoutMs;
    else delete row.timeoutMs;

    const next = editing
      ? configured.map((item) => (item.name === editing.name ? row : item))
      : [...configured, row];
    setFormOpen(false);
    await persist(next);
    // The gateway dedupes servers by endpoint (`assembly wins`) and skips them
    // silently; the row would sit at 空闲 forever with no explanation (F5).
    if (!editing) {
      const result = await reloadConnectors().catch(() => null);
      if (result && !result.connectors.some((item) => item.name === row.name)) {
        setError(t('screen.capabilities.err.notApplied', { name: row.name }));
      }
    }
  };

  const remove = async (row: ConnectorRow) => {
    await persist(
      configured.filter((item) => item.name !== row.name),
      disabledNames.filter((name) => name !== row.name),
    );
  };

  const toggle = async (row: ConnectorRow) => {
    const disabled = disabledNames.includes(row.name)
      ? disabledNames.filter((name) => name !== row.name)
      : [...disabledNames, row.name];
    await persist(configured, disabled);
  };

  const connectorRows = configured.map((row) => {
    const connector = live.get(row.name);
    const disabled = disabledNames.includes(row.name);
    const state = connectorState(connector, disabled);
    return { row, connector, disabled, state };
  });

  return (
    <div className="zk-screen">
      <header className="zk-screen__header">
        <h1 className="zk-screen__title">{t('screen.capabilities.title')}</h1>
        <p className="zk-screen__note">{t('screen.capabilities.note')}</p>
        <div className="zk-screen__actions">
          {tab === 'connectors' ? (
            <Button type="primary" onClick={openAdd} data-testid="connector-add">
              {t('screen.capabilities.addConnector')}
            </Button>
          ) : null}
          <Button onClick={() => void refresh()} loading={busy} data-testid="capabilities-refresh">
            {t('screen.capabilities.refresh')}
          </Button>
        </div>
      </header>

      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : null}
      {notice ? (
        <div className="zk-notice" role="status">
          {notice}
        </div>
      ) : null}

      {data === null ? (
        <div className="zk-empty">
          <Spin />
        </div>
      ) : (
        <Tabs
          activeKey={tab}
          onChange={(key) => setSearchParams(key === 'skills' ? {} : { tab: key }, { replace: true })}
          items={[
            {
              key: 'skills',
              label: t('screen.capabilities.skills', { count: data.skills.length }),
              children:
                data.skills.length === 0 ? (
                  <Empty description={t('screen.capabilities.noSkills')}>
                    <Typography.Text type="secondary">
                      {t('screen.capabilities.noSkills.hint')}
                    </Typography.Text>
                  </Empty>
                ) : (
                  <Table
                    size="small"
                    rowKey="id"
                    pagination={false}
                    // Without a fixed layout the source column takes its content
                    // width and the name column is squeezed to one character per
                    // line — the ellipsis never engages (round-3 conn audit F2).
                    tableLayout="fixed"
                    scroll={{ x: 'max-content' }}
                    dataSource={data.skills}
                    columns={[
                      { title: t('screen.capabilities.col.name'), dataIndex: 'name', width: 160 },
                      { title: t('screen.capabilities.col.description'), dataIndex: 'description' },
                      {
                        title: t('screen.capabilities.col.source'),
                        dataIndex: 'source',
                        width: 200,
                        // Absolute paths used to widen the column until the
                        // description was one character per line (round-2 C-08).
                        render: (value: string) => (
                          <Typography.Text className="zk-connector-path" title={value} ellipsis>
                            {value}
                          </Typography.Text>
                        ),
                      },
                    ]}
                  />
                ),
            },
            {
              key: 'connectors',
              label: t('screen.capabilities.connectors', { count: configured.length }),
              children:
                configured.length === 0 ? (
                  <Empty
                    description={t('screen.capabilities.noConnectors')}
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                  >
                    <Button onClick={openAdd}>{t('screen.capabilities.addConnector')}</Button>
                  </Empty>
                ) : (
                  <ul className="zk-connector-list">
                    {connectorRows.map(({ row, connector, disabled, state }, index) => (
                      <li
                        key={`${row.name}:${index}`}
                        className="zk-connector-row"
                        data-testid="connector-row"
                        data-state={state.tone}
                      >
                        <div className="zk-connector-row__main">
                          <div className="zk-connector-row__title">
                            <span className="zk-state-dot" data-tone={state.tone} aria-hidden="true" />
                            <Typography.Text strong>{row.name}</Typography.Text>
                            <Tag variant="filled">{row.transport === 'stdio' ? 'stdio' : 'http'}</Tag>
                            <span className="zk-connector-row__state">{t(state.labelKey)}</span>
                          </div>
                          <Typography.Text
                            type="secondary"
                            className="zk-connector-path"
                            title={row.transport === 'stdio' ? (row.command ?? '') : (row.url ?? '')}
                            ellipsis
                          >
                            {row.transport === 'stdio' ? row.command : row.url}
                          </Typography.Text>
                          {connector && connector.tools > 0 ? (
                            <Typography.Text type="secondary">
                              {t('screen.capabilities.toolsConnected', { count: connector.tools })}
                            </Typography.Text>
                          ) : null}
                          {/* The failure sentence is the only actionable thing on
                              a broken row; it used to be dropped entirely. */}
                          {state.tone === 'error' && connector?.error ? (
                            <p className="zk-connector-row__error" role="status">
                              {connector.error}
                            </p>
                          ) : null}
                        </div>
                        <div className="zk-connector-row__actions">
                          <Button size="small" onClick={() => void toggle(row)} disabled={busy}>
                            {disabled
                              ? t('screen.capabilities.enable')
                              : t('screen.capabilities.disable')}
                          </Button>
                          <Button size="small" onClick={() => openEdit(row)} disabled={busy}>
                            {t('screen.capabilities.edit')}
                          </Button>
                          <Popconfirm
                            title={t('screen.capabilities.deleteConfirm', { name: row.name })}
                            okButtonProps={{ danger: true }}
                            onConfirm={() => void remove(row)}
                          >
                            <Button size="small" danger disabled={busy}>
                              {t('screen.capabilities.delete')}
                            </Button>
                          </Popconfirm>
                        </div>
                      </li>
                    ))}
                  </ul>
                ),
            },
          ]}
        />
      )}

      <Modal
        open={formOpen}
        title={editing ? t('screen.capabilities.editConnector') : t('screen.capabilities.addConnector')}
        onCancel={() => setFormOpen(false)}
        onOk={() => {
          submit().catch(() => undefined);
        }}
        okText={t('settings.save')}
        cancelText={t('settings.revert')}
        destroyOnHidden
        width={560}
      >
        <Form form={form} layout="vertical" preserve={false} requiredMark>
          <Form.Item
            name="name"
            label={t('screen.capabilities.field.name')}
            extra={t('screen.capabilities.help.name')}
            rules={[
              { required: true, whitespace: true, message: t('screen.capabilities.err.name') },
              { max: 80, message: t('screen.capabilities.err.nameLong') },
              {
                validator: (_, value: string) => {
                  const name = String(value ?? '').trim();
                  if (!name) return Promise.resolve();
                  // Two rows sharing a name share a live entry: the second one
                  // showed the first one's error and a delete removed both
                  // (round-3 conn audit F4).
                  const clash = configured.some(
                    (row) => row.name === name && row.name !== editing?.name,
                  );
                  return clash
                    ? Promise.reject(new Error(t('screen.capabilities.err.nameTaken')))
                    : Promise.resolve();
                },
              },
            ]}
          >
            <Input data-testid="connector-field-name" />
          </Form.Item>
          <Form.Item name="transport" label={t('screen.capabilities.field.transport')}>
            <Select
              options={[
                { value: 'http', label: t('screen.capabilities.transportHttp') },
                { value: 'stdio', label: t('screen.capabilities.transportStdio') },
              ]}
            />
          </Form.Item>
          <Form.Item
            noStyle
            shouldUpdate={(prev, next) => prev.transport !== next.transport}
          >
            {({ getFieldValue }) =>
              getFieldValue('transport') === 'stdio' ? (
                <>
                  <Form.Item
                    name="command"
                    label={t('screen.capabilities.field.command')}
                    rules={[{ required: true, whitespace: true, message: t('screen.capabilities.err.command') }]}
                  >
                    <Input placeholder="npx" />
                  </Form.Item>
                  <Form.Item name="argsText" label={t('screen.capabilities.field.args')} extra={t('screen.capabilities.help.lines')}>
                    <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
                  </Form.Item>
                  <Form.Item name="envText" label={t('screen.capabilities.field.env')} extra={t('screen.capabilities.help.lines')}>
                    <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
                  </Form.Item>
                </>
              ) : (
                <Form.Item
                  name="url"
                  label={t('screen.capabilities.field.url')}
                  rules={[
                    { required: true, whitespace: true, message: t('screen.capabilities.err.url') },
                    {
                      validator: (_, value: string) =>
                        !value || /^https?:\/\/[^\s]+$/.test(value)
                          ? Promise.resolve()
                          : Promise.reject(new Error(t('screen.capabilities.err.url'))),
                    },
                  ]}
                >
                  <Input placeholder="https://example.com/mcp" />
                </Form.Item>
              )
            }
          </Form.Item>
          <Form.Item name="timeoutMs" label={t('screen.capabilities.field.timeout')} extra={t('screen.capabilities.help.timeout')}>
            <InputNumber min={0} step={1000} style={{ width: '100%' }} suffix="ms" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}