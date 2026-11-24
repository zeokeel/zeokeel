import { useEffect, useState } from 'react';
import {
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Tag,
  Typography,
} from 'antd';
import { setSetting, subscribeSettings, type SettingsRows } from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';

/**
 * The model catalogue: the one place a model is defined, and the one place the
 * default is chosen.
 *
 * Before this, the page had two controls writing the same row (`chat.model`):
 * a 「设为默认」 button in the list and a free-text 模型 field below it, so the
 * result depended on the order the user touched them (round-2 S5). Now the list
 * owns the catalogue and a single picker owns the default.
 *
 * The rows are read from `modelcatalog.models` itself, not from the derived
 * `/api/models` view, so saving keeps every field the form does not touch
 * (custom thinking modes, prices, anything a newer backend adds). A row's key is
 * write-only: it never travels back to the browser, so an empty field means
 * "keep what is stored".
 */

/** One raw catalog row: unknown fields are preserved on save. */
type CatalogRow = Record<string, unknown> & {
  id: string;
  name: string;
  note?: string;
  contextWindow?: number;
  baseURL?: string;
  apiKey?: string;
  family?: string;
  defaultThinking?: string;
  temperature?: number;
  maxTokens?: number;
};

const FAMILIES = ['', 'qwen', 'deepseek', 'other', 'custom'] as const;

/** What the stored value actually is: absent, an array, or hand-broken JSON. */
type Parsed = { rows: CatalogRow[]; broken: boolean };

function parseRows(raw: string): Parsed {
  if (!raw.trim()) return { rows: [], broken: false };
  try {
    const decoded = JSON.parse(raw);
    if (!Array.isArray(decoded)) return { rows: [], broken: true };
    // `[null]`, `[{}]` and `[1,2,3]` are valid JSON but not rows: rendering
    // them crashed the whole settings tree on `row.id` (round-3 P0-B). A row
    // needs an object with a non-empty string id; anything else is reported as
    // broken so the user gets the recovery action instead of a white page.
    const rows: CatalogRow[] = [];
    for (const entry of decoded) {
      if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
        return { rows: [], broken: true };
      }
      const id = (entry as Record<string, unknown>).id;
      if (typeof id !== 'string' || id.trim() === '') return { rows: [], broken: true };
      rows.push(entry as CatalogRow);
    }
    return { rows, broken: false };
  } catch {
    return { rows: [], broken: true };
  }
}

/** The id shape the backend's lookup accepts: no spaces, no CJK, bounded. */
const ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,119}$/;

export function ModelCatalogEditor() {
  const t = useT();
  const [rows, setRows] = useState<SettingsRows>({});
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<CatalogRow | null>(null);
  const [opening, setOpening] = useState(false);
  const [form] = Form.useForm<CatalogRow>();

  useEffect(() => subscribeSettings((value) => setRows(value)), []);

  const raw = String(rows[SETTING_KEYS.modelCatalogModels] ?? '');
  const parsed = parseRows(raw);
  const catalog = parsed.rows;
  const configured = raw.trim() !== '';
  const current = String(rows[SETTING_KEYS.chatModel] ?? '');
  const currentExists = catalog.some((row) => String(row.id) === current);

  const save = async (next: CatalogRow[]) => {
    try {
      await setSetting(SETTING_KEYS.modelCatalogModels, JSON.stringify(next));
      setError(null);
    } catch (cause) {
      setError(describeError(cause, t, 'settings.model.saveFailed'));
    }
  };

  const setDefault = async (id: string) => {
    try {
      await setSetting(SETTING_KEYS.chatModel, id);
      setError(null);
    } catch (cause) {
      setError(describeError(cause, t, 'settings.model.saveFailed'));
    }
  };

  const openAdd = () => {
    setEditing(null);
    form.setFieldsValue({
      id: '',
      name: '',
      baseURL: '',
      apiKey: '',
      contextWindow: 0,
      note: '',
      family: '',
      maxTokens: 0,
    });
    setOpening(true);
  };

  const openEdit = (row: CatalogRow) => {
    setEditing(row);
    form.setFieldsValue({ ...row, apiKey: '' });
    setOpening(true);
  };

  const submit = async () => {
    const values = await form.validateFields();
    const row: CatalogRow = { ...((editing ?? { id: '', name: '' }) as CatalogRow) };
    for (const [key, value] of Object.entries(values)) {
      if (value === undefined || value === '') {
        if (key === 'apiKey') continue; // write-only: blank keeps the stored key
        delete row[key];
      } else {
        row[key] = value;
      }
    }
    const next: CatalogRow[] = editing
      ? catalog.map((item) => (item.id === editing.id ? row : item))
      : [...catalog, row];
    setOpening(false);
    await save(next);
    // The first model in an empty catalogue becomes the default: otherwise the
    // user adds a model, the picker still says "未设置", and the composer keeps
    // running on whatever `chat.model` used to point at (round-2 S3/S5).
    if (!editing && catalog.length === 0) await setDefault(String(row.id));
  };

  /**
   * Removing the default also moves the default: leaving `chat.model` pointing at
   * a row that no longer exists made the composer show an id its own list did not
   * contain, and the next turn silently ran on the catalogue's first row
   * (round-2 S4). The confirm says which model takes over before it happens.
   */
  const remove = async (row: CatalogRow) => {
    const next = catalog.filter((item) => item.id !== row.id);
    await save(next);
    // Whatever was default stops existing in two cases: the deleted row was the
    // default, or the default was already stale and this was the last row.
    const stillExists = next.some((item) => item.id === current);
    if (!stillExists) await setDefault(next[0] ? String(next[0].id) : '');
  };

  return (
    <div className="zk-model-catalog" data-testid="model-catalog">
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : null}

      {parsed.broken ? (
        <div className="zk-model-broken" role="alert" data-testid="model-broken">
          <p>{t('settings.model.broken')}</p>
          <p className="zk-model-broken__hint">{t('settings.model.brokenHint')}</p>
          <Space size={8}>
            <Button size="small" onClick={() => void save([])} data-testid="model-restore">
              {t('settings.model.restore')}
            </Button>
          </Space>
        </div>
      ) : null}

      {!parsed.broken ? (
        <>
          {/* One control for the default. Disabled until a row exists, with the
              reason spelled out instead of an empty dropdown. */}
          <div className="zk-model-default">
            <label className="zk-field__label" htmlFor="zk-model-default-picker">
              {t('settings.model.defaultLabel')}
            </label>
            <Select
              id="zk-model-default-picker"
              className="zk-model-default__picker"
              data-testid="model-default-picker"
              value={currentExists ? current : undefined}
              placeholder={t('settings.model.defaultUnset')}
              disabled={catalog.length === 0}
              options={catalog.map((row) => ({
                value: String(row.id),
                label: String(row.name || row.id),
              }))}
              onChange={(id: string) => void setDefault(id)}
            />
          </div>
          {configured && current !== '' && !currentExists ? (
            <p className="zk-model-stale" role="status">
              {t('settings.model.staleDefault', { id: current })}
            </p>
          ) : null}

          {catalog.length === 0 ? (
            <p className="zk-model-empty">
              {configured ? t('settings.model.cleared') : t('settings.model.empty')}
            </p>
          ) : null}

          <ul className="zk-model-list">
            {catalog.map((row) => (
              <li key={row.id} className="zk-model-row" data-testid="model-row">
                <div className="zk-model-row__main">
                  <Space size={6}>
                    <Typography.Text strong>{String(row.name || row.id)}</Typography.Text>
                    {String(row.id) === current ? (
                      <Tag color="blue">{t('settings.model.default')}</Tag>
                    ) : null}
                  </Space>
                  <Typography.Text type="secondary">
                    {[
                      row.id,
                      (row.baseURL as string) || t('settings.model.inheritProvider'),
                      row.apiKey ? t('settings.model.keySet') : t('settings.model.keyMissing'),
                      typeof row.contextWindow === 'number' && Number.isFinite(row.contextWindow) && row.contextWindow > 0
                        ? `${Math.round(row.contextWindow / 1024)}k`
                        : '',
                      row.family ? `${t('settings.model.family')}: ${row.family}` : '',
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </Typography.Text>
                </div>
                <Space size={4}>
                  {String(row.id) === current ? null : (
                    <Button size="small" onClick={() => void setDefault(String(row.id))}>
                      {t('settings.model.makeDefault')}
                    </Button>
                  )}
                  <Button size="small" onClick={() => openEdit(row)}>
                    {t('settings.model.edit')}
                  </Button>
                  <Popconfirm
                    title={t('settings.model.deleteConfirm', { name: String(row.name || row.id) })}
                    description={
                      String(row.id) === current
                        ? t('settings.model.deleteDefaultHint', {
                            next: catalog.find((item) => item.id !== row.id)
                              ? String(
                                  catalog.find((item) => item.id !== row.id)?.name ??
                                    catalog.find((item) => item.id !== row.id)?.id,
                                )
                              : t('settings.model.noneLeft'),
                          })
                        : undefined
                    }
                    okButtonProps={{ danger: true }}
                    onConfirm={() => void remove(row)}
                  >
                    <Button size="small" danger>
                      {t('settings.model.delete')}
                    </Button>
                  </Popconfirm>
                </Space>
              </li>
            ))}
          </ul>

          <Button onClick={openAdd} data-testid="model-add">
            {t('settings.model.add')}
          </Button>
        </>
      ) : null}

      <Modal
        open={opening}
        title={editing ? t('settings.model.editTitle') : t('settings.model.addTitle')}
        onCancel={() => setOpening(false)}
        onOk={() => void submit()}
        okText={t('settings.model.save')}
        cancelText={t('settings.model.cancel')}
        destroyOnHidden
        width={560}
        // The form is taller than a 900px window; anchoring the footer to the
        // viewport keeps 保存 reachable without scrolling the modal body first.
        styles={{ body: { maxHeight: 'min(56vh, 520px)', overflowY: 'auto' } }}
      >
        <Form form={form} layout="vertical" preserve={false} requiredMark>
          <Form.Item
            name="name"
            label={t('settings.model.field.name')}
            rules={[{ required: true, whitespace: true, message: t('settings.model.err.name') }]}
          >
            <Input data-testid="model-field-name" />
          </Form.Item>
          <Form.Item
            name="id"
            label={t('settings.model.field.id')}
            extra={t('settings.model.help.id')}
            rules={[
              { required: true, whitespace: true, message: t('settings.model.err.idRequired') },
              {
                validator: (_, value: string) => {
                  if (!value) return Promise.resolve();
                  if (!ID_PATTERN.test(value)) {
                    return Promise.reject(new Error(t('settings.model.err.idPattern')));
                  }
                  const clash = catalog.some(
                    (row) => row.id === value && row.id !== editing?.id,
                  );
                  return clash
                    ? Promise.reject(new Error(t('settings.model.err.idDuplicate')))
                    : Promise.resolve();
                },
              },
            ]}
          >
            {/* The id is identity: renaming it would orphan every session that
                pinned it. Copy-as-new is the sanctioned path. */}
            <Input placeholder="deepseek-chat" disabled={editing !== null} />
          </Form.Item>
          <Form.Item
            name="baseURL"
            label={t('settings.model.field.baseURL')}
            extra={t('settings.model.help.baseURL')}
            rules={[
              {
                validator: (_, value: string) => {
                  if (!value) return Promise.resolve();
                  return /^https?:\/\/[^\s]+$/.test(value)
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('settings.model.err.baseURL')));
                },
              },
            ]}
          >
            <Input placeholder="https://api.deepseek.com/v1" />
          </Form.Item>
          <Form.Item
            name="apiKey"
            label={t('settings.model.field.apiKey')}
            extra={editing?.apiKey ? t('settings.model.help.apiKeySet') : t('settings.model.help.apiKeyUnset')}
          >
            <Input.Password autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="contextWindow"
            label={t('settings.model.field.contextWindow')}
            rules={[
              {
                validator: (_, value: number | undefined) => {
                  if (value === undefined || value === null || value === 0) return Promise.resolve();
                  return Number.isInteger(value) && value >= 1024 && value <= 2_000_000
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('settings.model.err.contextWindow')));
                },
              },
            ]}
          >
            <InputNumber min={0} step={1024} style={{ width: '100%' }} suffix="tokens" />
          </Form.Item>
          <Form.Item name="family" label={t('settings.model.field.family')} extra={t('settings.model.help.family')}>
            <Select options={FAMILIES.map((value) => ({ value, label: value || t('settings.model.familyAuto') }))} />
          </Form.Item>
          <Form.Item
            name="maxTokens"
            label={t('settings.model.field.maxTokens')}
            dependencies={['contextWindow']}
            rules={[
              ({ getFieldValue }) => ({
                validator: (_, value: number | undefined) => {
                  const window = Number(getFieldValue('contextWindow') ?? 0);
                  if (!value || !window) return Promise.resolve();
                  if (!Number.isInteger(value)) {
                    return Promise.reject(new Error(t('settings.model.err.integer')));
                  }
                  return value <= window
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('settings.model.err.maxTokens')));
                },
              }),
            ]}
          >
            <InputNumber min={0} style={{ width: '100%' }} suffix="tokens" />
          </Form.Item>
          <Form.Item name="note" label={t('settings.model.field.note')}>
            <Input placeholder={t('settings.model.help.note')} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}