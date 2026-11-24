import { useEffect, useState } from 'react';
import { Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Tag, Typography } from 'antd';
import { setSetting, subscribeSettings, type SettingsRows } from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';

/**
 * The model list (native 「模型列表」): one row per catalog entry with 编辑/删除, plus
 * 添加模型 — instead of the JSON textarea this page used to show.
 *
 * The rows are read from the `modelcatalog.models` settings row itself, not from the
 * derived /api/models view, so saving keeps every field the form does not touch
 * (custom thinking modes, prices, anything a newer backend adds). The API key is
 * write-only: the row's key never travels to the browser, so an empty field means
 * "keep what is there".
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

function parseRows(raw: string): CatalogRow[] {
  if (!raw.trim()) return [];
  try {
    const decoded = JSON.parse(raw);
    return Array.isArray(decoded) ? (decoded as CatalogRow[]) : [];
  } catch {
    // A hand-edited broken row is reported, not silently replaced.
    return [];
  }
}

export function ModelCatalogEditor() {
  const t = useT();
  const [rows, setRows] = useState<SettingsRows>({});
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<CatalogRow | null>(null);
  const [opening, setOpening] = useState(false);
  const [form] = Form.useForm<CatalogRow>();

  useEffect(() => subscribeSettings((value) => setRows(value)), []);

  const raw = String(rows[SETTING_KEYS.modelCatalogModels] ?? '');
  const catalog = parseRows(raw);
  const current = String(rows[SETTING_KEYS.chatModel] ?? '');

  const save = async (next: CatalogRow[]) => {
    try {
      await setSetting(SETTING_KEYS.modelCatalogModels, JSON.stringify(next));
      setError(null);
    } catch (cause) {
      setError(describeError(cause, t, 'settings.model.saveFailed'));
    }
  };

  const openAdd = () => {
    setEditing(null);
    form.setFieldsValue({ id: '', name: '', baseURL: '', apiKey: '', contextWindow: 0, note: '', family: '', maxTokens: 0 });
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
  };

  const remove = async (row: CatalogRow) => {
    await save(catalog.filter((item) => item.id !== row.id));
  };

  const makeDefault = async (row: CatalogRow) => {
    try {
      await setSetting(SETTING_KEYS.chatModel, String(row.id));
    } catch (cause) {
      setError(describeError(cause, t, 'settings.model.saveFailed'));
    }
  };

  return (
    <div className="zk-model-catalog" data-testid="model-catalog">
      {error ? (
        <div className="zk-msg-error" role="alert">
          {error}
        </div>
      ) : null}
      {raw.trim() && catalog.length === 0 ? (
        <div className="zk-msg-error" role="alert">
          {t('settings.model.broken')}
        </div>
      ) : null}
      {catalog.length === 0 && !raw.trim() ? (
        // "Nothing here" is a state, not an absence: say what the list is for and
        // what happens after the first row (round-2 S2/F-6 — the composer's
        // model picker stays empty until a row exists).
        <p className="zk-model-empty">{t('settings.model.empty')}</p>
      ) : null}
      <ul className="zk-model-list">
        {catalog.map((row) => (
          <li key={row.id} className="zk-model-row" data-testid="model-row">
            <div className="zk-model-row__main">
              <Space size={6}>
                <Typography.Text strong>{String(row.name || row.id)}</Typography.Text>
                {row.id === current ? <Tag color="blue">{t('settings.model.default')}</Tag> : null}
              </Space>
              <Typography.Text type="secondary">
                {[
                  row.id,
                  (row.baseURL as string) || t('settings.model.inheritProvider'),
                  row.apiKey ? t('settings.model.keySet') : t('settings.model.keyMissing'),
                  row.contextWindow ? `${Math.round(row.contextWindow / 1024)}k` : '',
                  row.family ? `${t('settings.model.family')}: ${row.family}` : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </Typography.Text>
            </div>
            <Space size={4}>
              {row.id === current ? null : (
                <Button size="small" onClick={() => void makeDefault(row)}>
                  {t('settings.model.makeDefault')}
                </Button>
              )}
              <Button size="small" onClick={() => openEdit(row)}>
                {t('settings.model.edit')}
              </Button>
              <Popconfirm
                title={t('settings.model.deleteConfirm', { name: String(row.name || row.id) })}
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

      <Modal
        open={opening}
        title={editing ? t('settings.model.editTitle') : t('settings.model.addTitle')}
        onCancel={() => setOpening(false)}
        onOk={() => void submit()}
        okText={t('settings.model.save')}
        cancelText={t('settings.model.cancel')}
        destroyOnHidden
        width={560}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item name="name" label={t('settings.model.field.name')} rules={[{ required: true, whitespace: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="id" label={t('settings.model.field.id')} rules={[{ required: true, whitespace: true }]}>
            <Input placeholder="deepseek-chat" disabled={editing !== null} />
          </Form.Item>
          <Form.Item name="baseURL" label={t('settings.model.field.baseURL')} extra={t('settings.model.help.baseURL')}>
            <Input placeholder="https://api.deepseek.com/v1" />
          </Form.Item>
          <Form.Item
            name="apiKey"
            label={t('settings.model.field.apiKey')}
            extra={editing?.apiKey ? t('settings.model.help.apiKeySet') : t('settings.model.help.apiKeyUnset')}
          >
            <Input.Password autoComplete="off" />
          </Form.Item>
          <Form.Item name="contextWindow" label={t('settings.model.field.contextWindow')}>
            <InputNumber min={0} step={1024} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="family" label={t('settings.model.field.family')} extra={t('settings.model.help.family')}>
            <Select options={FAMILIES.map((value) => ({ value, label: value || t('settings.model.familyAuto') }))} />
          </Form.Item>
          <Form.Item name="maxTokens" label={t('settings.model.field.maxTokens')}>
            <InputNumber min={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="note" label={t('settings.model.field.note')}>
            <Input placeholder={t('settings.model.help.note')} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
