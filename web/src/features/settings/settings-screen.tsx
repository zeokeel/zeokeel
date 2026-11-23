import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Input, InputNumber, Menu, Radio, Switch, Typography } from 'antd';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import {
  changedRows,
  defaultPageKey,
  findPage,
  orderedPages,
  pageValues,
  unmappedRows,
  type SettingsField,
  type SettingsPage,
} from './ia';
import {
  hasCapability,
  setSetting,
  subscribeSettings,
  type SettingsRows,
} from '@/api/gateway';
import { useEscClaim } from '@/keyboard/keyboard-provider';
import { ZkIcon } from '@/design/icons';
import { ConfirmDanger } from '@/ui/confirm-danger';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';

/**
 * R12–R21 · Settings — nine pages over the real `settings` domain.
 *
 * Three behaviours are the client's own:
 *  - the dirty guard (B1/B2/E5): Esc on a dirty page opens 丢弃/继续编辑;
 *  - a sticky footer with the page's single primary action (B4);
 *  - one `settings.set` per changed row. The gateway pushes `settings.delta`
 *    after the write, so "已保存" appears when the domain echoes the value —
 *    never after a fixed delay, and never merely because the RPC returned.
 */
export function SettingsScreen() {
  const t = useT();
  const { page: pageKey } = useParams<{ page: string }>();
  const navigate = useNavigate();
  const [rows, setRows] = useState<SettingsRows>({});
  const [connected, setConnected] = useState(false);
  const [navQuery, setNavQuery] = useState('');
  const canWrite = hasCapability('settings.set');

  useEffect(() => {
    const unsubscribe = subscribeSettings((value) => {
      setRows(value);
      setConnected(true);
    });
    return unsubscribe;
  }, []);

  const pages = useMemo(() => orderedPages(), []);
  const page = pageKey ? findPage(pageKey) : undefined;

  // `/settings` with no page (or an unknown one) lands on the first page.
  useEffect(() => {
    if (!pageKey || !findPage(pageKey)) {
      navigate(`/settings/${defaultPageKey()}`, { replace: true });
    }
  }, [pageKey, navigate]);

  // B3: the search matches page names **and field labels**, and a field hit
  // navigates to its page with `?focus=<row key>` so the form can scroll to it.
  const matches = useMemo(() => {
    const needle = navQuery.trim().toLowerCase();
    const hits = new Map<string, string[]>();
    for (const page of pages) {
      if (needle === '') {
        hits.set(page.key, []);
        continue;
      }
      if (t(page.labelKey).toLowerCase().includes(needle)) {
        hits.set(page.key, []);
        continue;
      }
      const fields = page.sections
        .flatMap((section) => section.fields)
        .filter((field) => t(field.labelKey).toLowerCase().includes(needle));
      if (fields.length > 0) hits.set(page.key, fields.map((field) => field.key));
    }
    return hits;
  }, [pages, navQuery]);

  const navItems = useMemo(() => {
    const groups = new Map<
      string,
      { key: string; labelKey: MessageKey; rank: number; pages: SettingsPage[] }
    >();
    for (const item of pages) {
      if (!matches.has(item.key)) continue;
      const group = groups.get(item.group.key) ?? { ...item.group, pages: [] };
      group.pages.push(item);
      groups.set(item.group.key, group);
    }
    return [...groups.values()]
      .sort((a, b) => a.rank - b.rank)
      .map((group) => ({
        key: group.key,
        label: t(group.labelKey),
        type: 'group' as const,
        children: group.pages.map((item) => ({
          key: item.key,
          label: t(item.labelKey),
          // Field-level hits say which field matched, so 「主题」 lands on 外观
          // instead of silently showing the whole page.
          ...(matches.get(item.key)?.length
            ? { extra: t('settings.fieldHits', { count: matches.get(item.key)?.length ?? 0 }) }
            : {}),
        })),
      }))
      .filter((group) => group.children.length > 0);
  }, [pages, matches]);

  return (
    <div className="zk-settings">
      <div className="zk-settings__nav">
        <Input
          size="small"
          prefix={<ZkIcon name="search" />}
          placeholder={t('settings.search')}
          aria-label={t('settings.search')}
          value={navQuery}
          onChange={(event) => setNavQuery(event.target.value)}
          allowClear
        />
        <Menu
          mode="inline"
          selectedKeys={pageKey ? [pageKey] : []}
          items={navItems}
          onClick={({ key }) => {
            const focus = matches.get(String(key))?.[0];
            navigate(`/settings/${key}${focus ? `?focus=${encodeURIComponent(focus)}` : ''}`);
          }}
        />
      </div>

      {page ? (
        <SettingsPageForm
          key={page.key}
          page={page}
          rows={rows}
          connected={connected}
          canWrite={canWrite}
          extraRows={unmappedRows(rows)}
        />
      ) : (
        <div className="zk-empty">{t('settings.loading')}</div>
      )}
    </div>
  );
}

function SettingsPageForm({
  page,
  rows,
  connected,
  canWrite,
  extraRows,
}: {
  page: SettingsPage;
  rows: SettingsRows;
  connected: boolean;
  canWrite: boolean;
  extraRows: string[];
}) {
  const t = useT();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const focusRef = useFocusField(searchParams.get('focus'));
  const [baseline, setBaseline] = useState<Record<string, string>>(() =>
    pageValues(page, rows),
  );
  const [values, setValues] = useState<Record<string, string>>(() => pageValues(page, rows));
  const [discardOpen, setDiscardOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [savedRows, setSavedRows] = useState<Array<{ key: string; value: string }>>([]);

  const dirty = useMemo(
    () => JSON.stringify(values) !== JSON.stringify(baseline),
    [values, baseline],
  );

  // Adopt values that arrived from the gateway while the page is clean. The
  // state update sits behind an await boundary (the react compiler lint forbids
  // a synchronous setState in an effect, and this also coalesces deltas).
  useEffect(() => {
    if (dirty) return;
    const next = pageValues(page, rows);
    if (JSON.stringify(next) === JSON.stringify(baseline)) return;
    void Promise.resolve().then(() => {
      setBaseline(next);
      setValues(next);
    });
  }, [rows, page, dirty, baseline]);

  // The pushed delta is the acknowledgement: once every row we sent is echoed
  // back, the notice flips from "等待回声" to "已保存".
  useEffect(() => {
    if (savedRows.length === 0) return;
    const echoed = savedRows.every((row) => (rows[row.key] ?? '') === row.value);
    if (!echoed) return;
    void Promise.resolve().then(() => {
      setNotice(t('settings.savedDone'));
      setSavedRows([]);
    });
  }, [rows, savedRows]);

  // B2: Esc inside settings always means "leave this page" — a dirty page asks
  // first (B1), a clean one just closes instead of swallowing the key.
  useEscClaim({
    id: 'settings-dirty-guard',
    level: 'settingsDirtyGuard',
    active: true,
    onEscape: () => {
      if (dirty) {
        setDiscardOpen(true);
        return true;
      }
      navigate('/');
      return true;
    },
  });

  const setValue = (key: string, value: string) => {
    setValues((current) => ({ ...current, [key]: value }));
  };

  const changes = changedRows(page, values, rows);

  const save = async () => {
    if (changes.length === 0) return;
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      for (const change of changes) await setSetting(change.key, change.value);
      setBaseline(values);
      setSavedRows(changes);
      setNotice(t('settings.submitted', { count: changes.length }));
    } catch (cause) {
      setError(describeError(cause, t, 'settings.saveFailed'));
    } finally {
      setSaving(false);
    }
  };

  const close = () => {
    if (dirty) setDiscardOpen(true);
    else navigate('/');
  };

  return (
    <div className="zk-settings__page">
      <header className="zk-settings__header">
        <div>
          <h1 className="zk-screen__title">{t(page.labelKey)}</h1>
          <Typography.Text type="secondary">
            {connected ? t('settings.source.live') : t('settings.source.connecting')}
            {canWrite ? '' : t('settings.readonly')}
          </Typography.Text>
        </div>
        <Button size="small" type="text" onClick={close} aria-label={t('settings.close')}>
          <ZkIcon name="x" />
        </Button>
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

      <div className="zk-settings__body">
        {page.sections.map((section, index) => (
          <section key={index} className="zk-settings__section">
            {section.titleKey ? <h2 className="zk-settings__section-title">{t(section.titleKey)}</h2> : null}
            {section.fields.length === 0 ? (
              <Typography.Text type="secondary">{t('settings.pageEmpty')}</Typography.Text>
            ) : null}
            {section.fields.map((field) => (
              <FieldRow
                key={field.key}
                field={field}
                focusRef={focusRef}
                value={values[field.key] ?? ''}
                disabled={!canWrite}
                onChange={(value) => setValue(field.key, value)}
              />
            ))}
          </section>
        ))}

        {extraRows.length > 0 ? (
          <section className="zk-settings__section">
            <h2 className="zk-settings__section-title">{t('settings.unmapped')}</h2>
            <ul className="zk-settings__unmapped">
              {extraRows.slice(0, 40).map((key) => (
                <li key={key}>
                  <code>{key}</code>
                  <span>{rows[key]}</span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}
      </div>

      <footer className="zk-settings__footer">
        <Typography.Text type="secondary">
          {dirty ? t('settings.unsaved') : t('settings.saved')}
        </Typography.Text>
        <div className="zk-settings__footer-actions">
          <Button onClick={() => setValues(baseline)} disabled={!dirty}>
            {t('settings.revert')}
          </Button>
          <Button
            type="primary"
            onClick={() => void save()}
            disabled={!dirty || !canWrite}
            loading={saving}
          >
            {t('settings.save')}
          </Button>
        </div>
      </footer>

      <ConfirmDanger
        open={discardOpen}
        title={t('settings.discard.title')}
        description={t('settings.discard.description')}
        confirmLabel={t('settings.discard.confirm')}
        onCancel={() => setDiscardOpen(false)}
        onConfirm={() => {
          setDiscardOpen(false);
          setValues(baseline);
          navigate('/');
        }}
      />
    </div>
  );
}

/** Scroll the field named by `?focus=` into view and mark it briefly (B3). */
function useFocusField(focusKey: string | null): (node: HTMLElement | null) => void {
  return useCallback(
    (node: HTMLElement | null) => {
      if (!node || !focusKey || node.dataset.fieldKey !== focusKey) return;
      node.scrollIntoView?.({ block: 'center' });
      node.dataset.focused = 'true';
    },
    [focusKey],
  );
}

function FieldRow({
  field,
  focusRef,
  value,
  disabled,
  onChange,
}: {
  field: SettingsField;
  focusRef: (node: HTMLElement | null) => void;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const t = useT();
  const id = `zk-field-${field.key}`;
  return (
    <div className="zk-field" data-field-key={field.key} ref={focusRef}>
      <div className="zk-field__label-row">
        <label className="zk-field__label" htmlFor={id}>
          {t(field.labelKey)}
        </label>
        {field.helpKey ? (
          <span className="zk-field__help">{t(field.helpKey)}</span>
        ) : null}
      </div>
      <div className="zk-field__control">
        {field.kind === 'text' ? (
          <Input
            id={id}
            value={value}
            disabled={disabled}
            placeholder={field.example}
            onChange={(event) => onChange(event.target.value)}
          />
        ) : null}
        {field.kind === 'textarea' ? (
          <Input.TextArea
            id={id}
            value={value}
            disabled={disabled}
            autoSize={{ minRows: 2, maxRows: 8 }}
            onChange={(event) => onChange(event.target.value)}
          />
        ) : null}
        {field.kind === 'number' ? (
          <InputNumber
            id={id}
            value={value === '' ? null : Number(value)}
            disabled={disabled}
            onChange={(next) => onChange(next === null || next === undefined ? '' : String(next))}
          />
        ) : null}
        {field.kind === 'switch' ? (
          <Switch
            id={id}
            checked={value === 'true'}
            disabled={disabled}
            onChange={(next) => onChange(next ? 'true' : 'false')}
          />
        ) : null}
        {field.kind === 'radio' ? (
          <Radio.Group
            value={value}
            disabled={disabled}
            onChange={(event) => onChange(String(event.target.value))}
            options={(field.options ?? []).map((option) => ({
              value: option.value,
              label: t(option.labelKey),
            }))}
          />
        ) : null}
      </div>
    </div>
  );
}