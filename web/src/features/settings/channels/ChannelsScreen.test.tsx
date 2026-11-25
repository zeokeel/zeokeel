import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nProvider } from '@/i18n';

/**
 * R27a · the channel settings screen (credentials, webhook sources, notify
 * routes).
 *
 * These tests pin the four things that make the surface trustworthy:
 *  1. each section renders what its API returned, and reads with the right
 *     parameters (the credential list is filtered by the chosen kind);
 *  2. a save sends the exact payload the contract allows — and the secret is
 *     gone from the form afterwards;
 *  3. a stored secret is **never** on the page: the list only carries
 *     identities, and the form drops its copy after the write;
 *  4. a delete goes through a confirmation, a route save round-trips the
 *     snapshot's own values, and a failed read is reported with 重试.
 *
 * No timers are faked: the screen does not poll, so every assertion here is
 * about one explicit read or one explicit write.
 */

const api = vi.hoisted(() => ({
  listCredentials: vi.fn(),
  setCredential: vi.fn(),
  deleteCredential: vi.fn(),
  listWebhookSources: vi.fn(),
  setWebhookSource: vi.fn(),
  removeWebhookSource: vi.fn(),
  loadNotifyRoutes: vi.fn(),
  upsertNotifyRoute: vi.fn(),
}));

vi.mock('@/api/channels', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/channels')>();
  // Keep the real types/constants (the kind list drives the Select); only the
  // requests are replaced.
  return { ...actual, ...api };
});

import { CREDENTIAL_FIELD_NAMES } from '@/api/channels';
import { ChannelsScreen } from './ChannelsScreen';

function renderScreen() {
  return render(
    <I18nProvider>
      <ChannelsScreen />
    </I18nProvider>,
  );
}

/** The confirm button lives in a portal; find it inside the open popconfirm. */
async function confirmPopover(user: ReturnType<typeof userEvent.setup>) {
  const buttons = await waitFor(() => {
    const found = document.querySelector('.ant-popconfirm-buttons');
    if (!found) throw new Error('the confirmation is not open');
    return found as HTMLElement;
  });
  await user.click(within(buttons).getByRole('button', { name: /确认删除|Delete/ }));
}

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  api.listCredentials.mockResolvedValue([]);
  api.listWebhookSources.mockResolvedValue([]);
  api.loadNotifyRoutes.mockResolvedValue([]);
  api.setCredential.mockResolvedValue(undefined);
  api.deleteCredential.mockResolvedValue(undefined);
  api.setWebhookSource.mockResolvedValue(undefined);
  api.removeWebhookSource.mockResolvedValue(undefined);
  api.upsertNotifyRoute.mockResolvedValue({
    category: 'alerts',
    primary: 'wechat',
    enabled: true,
  });
});

describe('ChannelsScreen — the three sections read their own source', () => {
  it('renders credentials, webhook sources and notify routes from the API', async () => {
    api.listCredentials.mockResolvedValue([{ kind: 'wechat', id: 'default' }]);
    api.listWebhookSources.mockResolvedValue([{ id: 'gh-1', channel: 'wechat' }]);
    api.loadNotifyRoutes.mockResolvedValue([
      { category: 'alerts', primary: 'wechat', enabled: true },
    ]);
    renderScreen();

    const credential = await screen.findByTestId('channels-credential-wechat-default');
    expect(credential).toBeInTheDocument();
    expect(credential).toHaveTextContent('wechat');
    expect(credential).toHaveTextContent('default');

    const source = await screen.findByTestId('channels-webhook-gh-1');
    expect(source).toHaveTextContent('gh-1');
    expect(source).toHaveTextContent('wechat');

    const route = await screen.findByTestId('channels-notify-alerts');
    expect(route).toBeInTheDocument();

    // The credential list is the one for the selected kind, not "everything".
    expect(api.listCredentials).toHaveBeenCalledWith('wechat');
    expect(api.listWebhookSources).toHaveBeenCalledTimes(1);
    expect(api.loadNotifyRoutes).toHaveBeenCalledTimes(1);
  });

  it('reads a channel only when nothing has been saved yet', async () => {
    renderScreen();
    expect(await screen.findByText(/还没有凭据|No credentials for this channel yet/)).toBeInTheDocument();
    expect(await screen.findByText(/还没有 Webhook 来源|No webhook sources yet/)).toBeInTheDocument();
    expect(await screen.findByText(/网关还没有通知类别|no notification categories/)).toBeInTheDocument();
  });
});

describe('ChannelsScreen — a credential is written once and never echoed', () => {
  it('sends the exact payload and clears the secret field', async () => {
    const user = userEvent.setup();
    api.listCredentials.mockResolvedValue([]);
    renderScreen();

    const token = await screen.findByTestId('channels-credential-field-token');
    await user.type(token, 's3cret-token');
    expect(token).toHaveValue('s3cret-token');

    await user.click(screen.getByTestId('channels-credential-save'));

    await waitFor(() =>
      expect(api.setCredential).toHaveBeenCalledWith({
        kind: 'wechat',
        id: 'default',
        fields: { token: 's3cret-token' },
      }),
    );
    // The form keeps no copy of a stored secret…
    await waitFor(() => expect(screen.getByTestId('channels-credential-field-token')).toHaveValue(''));
    // …and says what it stored (the identity, per the task's copy).
    expect(await screen.findByTestId('channels-credential-saved')).toHaveTextContent('default');
    // One re-read after the write, no polling.
    await waitFor(() => expect(api.listCredentials).toHaveBeenCalledTimes(2));
  });

  it('never puts a secret on the page after saving it', async () => {
    const user = userEvent.setup();
    api.listCredentials.mockResolvedValue([{ kind: 'wechat', id: 'default' }]);
    renderScreen();

    await user.type(await screen.findByTestId('channels-credential-field-token'), 'top-secret-value');
    await user.click(screen.getByTestId('channels-credential-save'));
    await waitFor(() => expect(api.setCredential).toHaveBeenCalled());

    // The stored row reappears…
    expect(await screen.findByTestId('channels-credential-wechat-default')).toBeInTheDocument();
    // …without the value anywhere in the document, in text or in an input.
    expect(document.body.textContent).not.toContain('top-secret-value');
    for (const input of document.querySelectorAll('input')) {
      expect((input as HTMLInputElement).value).not.toBe('top-secret-value');
    }
  });

  it('deletes only after the confirmation is accepted', async () => {
    const user = userEvent.setup();
    api.listCredentials.mockResolvedValue([{ kind: 'wechat', id: 'default' }]);
    renderScreen();

    await screen.findByTestId('channels-credential-wechat-default');
    await user.click(screen.getByTestId('channels-credential-delete-wechat-default'));
    // Nothing is deleted by merely opening the confirmation.
    expect(api.deleteCredential).not.toHaveBeenCalled();

    await confirmPopover(user);
    await waitFor(() =>
      expect(api.deleteCredential).toHaveBeenCalledWith({ kind: 'wechat', id: 'default' }),
    );
    await waitFor(() => expect(api.listCredentials).toHaveBeenCalledTimes(2));
  });
});

describe('ChannelsScreen — webhook sources', () => {
  it('adds a source and clears the secret input', async () => {
    const user = userEvent.setup();
    renderScreen();

    await user.type(await screen.findByTestId('channels-webhook-id-input'), 'gh-2');
    await user.type(screen.getByTestId('channels-webhook-secret-input'), 'sixteen-bytes-ok');
    await user.type(screen.getByTestId('channels-webhook-channel-input'), 'feishu');
    await user.click(screen.getByTestId('channels-webhook-add'));

    await waitFor(() =>
      expect(api.setWebhookSource).toHaveBeenCalledWith({
        id: 'gh-2',
        secret: 'sixteen-bytes-ok',
        channel: 'feishu',
      }),
    );
    await waitFor(() => expect(screen.getByTestId('channels-webhook-secret-input')).toHaveValue(''));
    expect(screen.getByTestId('channels-webhook-id-input')).toHaveValue('');
  });

  it('removes a source through the confirmation', async () => {
    const user = userEvent.setup();
    api.listWebhookSources.mockResolvedValue([{ id: 'gh-1', channel: 'wechat' }]);
    renderScreen();

    await screen.findByTestId('channels-webhook-gh-1');
    await user.click(screen.getByTestId('channels-webhook-delete-gh-1'));
    expect(api.removeWebhookSource).not.toHaveBeenCalled();

    await confirmPopover(user);
    await waitFor(() => expect(api.removeWebhookSource).toHaveBeenCalledWith('gh-1'));
    await waitFor(() => expect(api.listWebhookSources).toHaveBeenCalledTimes(2));
  });
});

describe('ChannelsScreen — notification routes', () => {
  it('saves the row it is showing, snapshot values included', async () => {
    const user = userEvent.setup();
    api.loadNotifyRoutes.mockResolvedValue([
      {
        category: 'alerts',
        primary: 'wechat',
        enabled: true,
        quietHours: '22:30-08:00',
      },
    ]);
    renderScreen();

    const route = await screen.findByTestId('channels-notify-alerts');
    await user.click(within(route).getByTestId('channels-notify-enabled-switch-alerts'));
    await user.click(within(route).getByTestId('channels-notify-save-alerts'));

    await waitFor(() =>
      expect(api.upsertNotifyRoute).toHaveBeenCalledWith({
        category: 'alerts',
        primary: 'wechat',
        enabled: false,
        quietHours: '22:30-08:00',
      }),
    );
    // The section re-reads the snapshot after the write.
    await waitFor(() => expect(api.loadNotifyRoutes).toHaveBeenCalledTimes(2));
  });

  it('shows the quiet-hours placeholder the copy promises', async () => {
    api.loadNotifyRoutes.mockResolvedValue([{ category: 'alerts', primary: 'inapp', enabled: true }]);
    renderScreen();
    const quiet = await screen.findByTestId('channels-notify-quiet-input-alerts');
    expect(quiet).toHaveAttribute('placeholder', '22:30-08:00');
  });
});

describe('ChannelsScreen — a failed read is reported, never hidden', () => {
  it('shows the cause and re-reads on 重试', async () => {
    const user = userEvent.setup();
    api.listCredentials
      .mockRejectedValueOnce(new Error('gateway said no'))
      .mockResolvedValue([{ kind: 'wechat', id: 'default' }]);
    renderScreen();

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('gateway said no');
    await user.click(within(alert).getByRole('button', { name: /重试|Retry/ }));

    // The retry is a real second read, and the list it produces is rendered.
    expect(await screen.findByTestId('channels-credential-wechat-default')).toBeInTheDocument();
    expect(api.listCredentials).toHaveBeenCalledTimes(2);
  });

  it('keeps a write failure next to its form without dropping the list', async () => {
    const user = userEvent.setup();
    api.listCredentials.mockResolvedValue([{ kind: 'feishu', id: 'bot' }]);
    api.setCredential.mockRejectedValueOnce(new Error('id already exists'));
    renderScreen();

    await user.type(await screen.findByTestId('channels-credential-field-token'), 'x1');
    // The kind default is wechat, so the feishu row is not on screen yet; the
    // failed write still names its own cause.
    await user.click(screen.getByTestId('channels-credential-save'));
    expect(await screen.findByText('id already exists')).toBeInTheDocument();
    expect(screen.getByTestId('channels-credential-field-token')).toHaveValue('x1');
  });
});

describe('ChannelsScreen — the credential field names are the contract', () => {
  it('uses the field keys each channel kind expects', () => {
    expect(CREDENTIAL_FIELD_NAMES.wechat).toEqual(['token']);
    expect(CREDENTIAL_FIELD_NAMES.dingtalk).toEqual(['token', 'signSecret']);
    expect(CREDENTIAL_FIELD_NAMES.feishu).toEqual(['appId', 'appSecret']);
    expect(CREDENTIAL_FIELD_NAMES.email).toEqual(['username', 'password']);
  });
});
