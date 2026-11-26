import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nProvider } from '@/i18n';
import {
  expectCanShrink,
  expectNoHorizontalOverflow,
  expectTapTarget,
  expectWrapsOrEllipsis,
  installViewport,
  PHONE_WIDTH,
  uninstallViewport,
} from '@/test/viewport';

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
 * No timers are faked for the three request/response sections: they do not
 * poll, so every assertion there is about one explicit read or one explicit
 * write. The 扫码接入 block is the exception — a QR login *is* a poll — and its
 * describe block owns the fake clock, because a 20 s real wait is the classic
 * flake when the machine is busy.
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
  startWechatLogin: vi.fn(),
  pollWechatLoginState: vi.fn(),
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
  api.startWechatLogin.mockResolvedValue({
    qrId: 'qr-1',
    qrUrl: 'https://wechat.example/qr/qr-1',
  });
  api.pollWechatLoginState.mockResolvedValue({ state: 'pending', connected: false });
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
    // DingTalk's send body ALWAYS carries `robotCode`, so a row without one is a
    // credential no send can use. The form must ask for it.
    expect(CREDENTIAL_FIELD_NAMES.dingtalk).toEqual(['token', 'robotCode']);
    expect(CREDENTIAL_FIELD_NAMES.feishu).toEqual(['appId', 'appSecret']);
    expect(CREDENTIAL_FIELD_NAMES.email).toEqual(['username', 'password']);
  });

  it('offers no field the retired callback signature used to need', () => {
    // `signSecret` signed DingTalk/Feishu callbacks. Those callbacks are gone, so
    // no adapter reads such a field: offering the box would ask for a secret
    // nothing consumes.
    const names = Object.values(CREDENTIAL_FIELD_NAMES).flat();
    expect(names).not.toContain('signSecret');
  });
});

/**
 * The DingTalk credential needs two identities, not one: the robot access token
 * and the robot code, which the platform's send API always receives
 * (`{"robotCode": ..., "openConversationId": ...}` — see
 * `internal/modules/dingtalk/adapter.go`). With only the token in the form, the
 * page saved a row the adapter could never use: the send went out with an empty
 * robotCode and the platform answered 400 while the UI had just said 已保存.
 */
describe('ChannelsScreen — DingTalk asks for the robot code the send API needs', () => {
  /** Pick a credential kind in the antd Select (its options live in a portal). */
  async function selectKind(kind: string) {
    const select = screen.getByTestId('channels-credential-kind-select');
    fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
    const option = document.querySelector(`.ant-select-item-option[title="${kind}"]`);
    expect(option).not.toBeNull();
    await act(async () => {
      fireEvent.click(option as Element);
    });
  }

  it('renders a robot code field for dingtalk', async () => {
    renderScreen();
    await screen.findByTestId('channels-credential-field-token');
    await selectKind('dingtalk');

    expect(await screen.findByTestId('channels-credential-field-robotCode')).toBeInTheDocument();
  });

  it('refuses a half-filled credential and sends both fields together', async () => {
    const user = userEvent.setup();
    renderScreen();
    await screen.findByTestId('channels-credential-field-token');
    await selectKind('dingtalk');

    await user.type(await screen.findByTestId('channels-credential-field-token'), 'access-token');
    // A token without a robot code cannot send, so it must not look saveable.
    expect(screen.getByTestId('channels-credential-save')).toBeDisabled();

    await user.type(screen.getByTestId('channels-credential-field-robotCode'), 'dingrobot1');
    await user.click(screen.getByTestId('channels-credential-save'));

    await waitFor(() =>
      expect(api.setCredential).toHaveBeenCalledWith({
        kind: 'dingtalk',
        id: 'default',
        fields: { token: 'access-token', robotCode: 'dingrobot1' },
      }),
    );
  });
});

/**
 * Screen 05 of docs/design/grokbot-ui/README.md: `/settings/channels` on a phone
 * over the LAN at ~390px. Two things broke there: a credential id / webhook
 * source id printed verbatim could widen the row, and 保存 — the screen's only
 * filled primary — was antd's `middle` size (32px), under a thumb.
 */
describe('ChannelsScreen at 390px', () => {
  const LONG_ID = `credential-${'d'.repeat(150)}`;

  beforeEach(() => {
    installViewport(PHONE_WIDTH);
  });

  afterEach(() => {
    uninstallViewport();
  });

  it('wraps long ids instead of widening the page', async () => {
    api.listCredentials.mockResolvedValue([{ kind: 'wechat', id: LONG_ID }]);
    api.listWebhookSources.mockResolvedValue([{ id: LONG_ID, channel: 'wechat' }]);

    renderScreen();

    const credential = await screen.findByTestId(`channels-credential-wechat-${LONG_ID}`);
    expectNoHorizontalOverflow(document.querySelector('.zk-screen') as Element);
    expectWrapsOrEllipsis(within(credential).getByText(LONG_ID), 'the credential id');
    // The id wraps *inside* a body that is allowed to shrink, so the row's own
    // action keeps its width instead of being pushed out of the row.
    expectCanShrink(credential.querySelector('.zk-inbox__body') as Element, 'the credential row body');
    expect(within(credential).getByRole('button', { name: /删\s*除|Delete/ })).toBeVisible();
  });

  it('keeps 保存 hittable', async () => {
    renderScreen();

    const save = await screen.findByTestId('channels-credential-save');
    expect(save).toBeVisible();
    expectTapTarget(save);
  });
});

/**
 * 扫码接入 (`wechat.login.start` → `wechat.login.state`).
 *
 * A QR login is the surface's only poll, and these tests pin the four rules
 * that make it a bounded one: it starts only when asked, it shows the code's
 * own URL, it stops on both terminal answers, and it never leaves a timer (or a
 * second request in flight) behind. The clock is fake throughout: a real 20 s
 * wait is a load test, not a correctness test.
 */
describe('ChannelsScreen — WeChat scan-to-connect', () => {
  const POLL_MS = 2000;

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  /** Let pending promises settle without moving the clock. */
  async function settle() {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
  }

  /** Move the fake clock, flushing the promises each tick produces. */
  async function advance(ms: number) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  }

  async function renderAndStart() {
    renderScreen();
    await settle();
    fireEvent.click(screen.getByTestId('channels-wechat-login-start'));
    await settle();
  }

  it('start shows the code URL and begins polling once per interval', async () => {
    await renderAndStart();

    expect(api.startWechatLogin).toHaveBeenCalledTimes(1);
    // The code's own URL, plus the note that says what to do with it: this
    // client ships no QR renderer, so the link is the honest stand-in.
    expect(screen.getByTestId('channels-wechat-login-url')).toHaveTextContent(
      'https://wechat.example/qr/qr-1',
    );
    expect(screen.getByText(/用微信扫码打开此链接|Scan this link with WeChat/)).toBeInTheDocument();
    // Disabled while the login is in flight, so a second code cannot be asked
    // for mid-poll and abandon the first one.
    expect(screen.getByTestId('channels-wechat-login-start')).toBeDisabled();

    // Nothing is polled before the first interval elapses.
    expect(api.pollWechatLoginState).not.toHaveBeenCalled();

    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenCalledWith('qr-1');
    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /等待扫码|Waiting for the scan/,
    );

    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(2);
    expect(api.pollWechatLoginState).toHaveBeenNthCalledWith(2, 'qr-1');
  });

  it('shows each state word and stops for good on confirmed', async () => {
    api.pollWechatLoginState
      .mockResolvedValueOnce({ state: 'pending', connected: false })
      .mockResolvedValueOnce({ state: 'waiting', connected: false })
      .mockResolvedValueOnce({ state: 'confirmed', connected: true });
    api.listCredentials.mockResolvedValue([{ kind: 'wechat', id: 'default' }]);

    await renderAndStart();
    expect(api.listCredentials).toHaveBeenCalledTimes(1);

    await advance(POLL_MS);
    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /等待扫码|Waiting for the scan/,
    );

    await advance(POLL_MS);
    // The intermediate state the human can act on: scanned, waiting to confirm.
    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /已扫码，请在手机上确认|Scanned/,
    );

    await advance(POLL_MS);
    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /已连接|Connected/,
    );
    // The credential the login just stored: the section re-reads instead of
    // assuming the row it thinks the gateway now holds. (`waitFor` is avoided
    // here on purpose: it would run on the real clock, which this test owns.)
    await settle();
    expect(api.listCredentials).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId('channels-wechat-login-start')).toBeEnabled();

    // A confirmed login stops the timer: no request follows it, however long
    // the page stays open.
    await advance(POLL_MS * 10);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(3);
  });

  it('stops on expired and lets the human generate a new code', async () => {
    api.pollWechatLoginState.mockResolvedValue({ state: 'expired', connected: false });

    await renderAndStart();
    await advance(POLL_MS);

    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /二维码已过期，重新生成|code expired/,
    );
    expect(screen.getByTestId('channels-wechat-login-start')).toBeEnabled();

    await advance(POLL_MS * 5);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);

    // The button is a real restart: a second code and a fresh poll.
    api.startWechatLogin.mockResolvedValue({
      qrId: 'qr-2',
      qrUrl: 'https://wechat.example/qr/qr-2',
    });
    fireEvent.click(screen.getByTestId('channels-wechat-login-start'));
    await settle();
    expect(screen.getByTestId('channels-wechat-login-url')).toHaveTextContent(
      'https://wechat.example/qr/qr-2',
    );
    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenLastCalledWith('qr-2');
  });

  it('stops polling after ten tries instead of waiting forever', async () => {
    await renderAndStart();

    await advance(POLL_MS * 10);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(10);
    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /已停止轮询|Polling stopped/,
    );
    expect(screen.getByTestId('channels-wechat-login-start')).toBeEnabled();

    await advance(POLL_MS * 5);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(10);
  });

  it('clears the timer when the page unmounts', async () => {
    const view = render(
      <I18nProvider>
        <ChannelsScreen />
      </I18nProvider>,
    );
    await settle();
    fireEvent.click(screen.getByTestId('channels-wechat-login-start'));
    await settle();

    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);

    view.unmount();
    await advance(POLL_MS * 10);
    // No request of any kind follows the unmount: the interval was cleared, and
    // the reply already in flight cannot reach a gone component.
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);
    expect(api.listCredentials).toHaveBeenCalledTimes(1);
  });

  it('clears the timer when the block goes away (another kind)', async () => {
    await renderAndStart();
    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);

    // 扫码接入 exists for WeChat only; switching away unmounts the block, and
    // an unmounted block must not keep asking about a code nobody can see.
    const select = screen.getByTestId('channels-credential-kind-select');
    fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
    const option = document.querySelector('.ant-select-item-option[title="dingtalk"]');
    expect(option).not.toBeNull();
    fireEvent.click(option as Element);
    await settle();

    expect(screen.queryByTestId('channels-wechat-login')).not.toBeInTheDocument();
    await advance(POLL_MS * 5);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);
  });

  it('reports a failed start and does not poll', async () => {
    api.startWechatLogin.mockRejectedValue(new Error('wechat login unavailable'));

    await renderAndStart();

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(/二维码生成失败|Could not generate the code/);
    expect(alert).toHaveTextContent('wechat login unavailable');
    expect(screen.queryByTestId('channels-wechat-login-url')).not.toBeInTheDocument();
    expect(screen.getByTestId('channels-wechat-login-start')).toBeEnabled();

    await advance(POLL_MS * 10);
    expect(api.pollWechatLoginState).not.toHaveBeenCalled();
  });

  it('stops polling when a state read fails, naming that failure', async () => {
    api.pollWechatLoginState.mockRejectedValue(new Error('gateway went away'));

    await renderAndStart();
    await advance(POLL_MS);

    const alert = screen.getByRole('alert');
    // A polling failure is not "could not generate the code": the code exists.
    expect(alert).toHaveTextContent(/查询扫码状态失败|Could not read the login state/);
    expect(alert).toHaveTextContent('gateway went away');
    expect(screen.getByTestId('channels-wechat-login-start')).toBeEnabled();

    await advance(POLL_MS * 5);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);
  });

  it('never has two state requests in the air at once', async () => {
    // A slow answer: the promise resolves only when this test says so.
    let release: ((value: { state: string; connected: boolean }) => void) | null = null;
    api.pollWechatLoginState
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            release = resolve;
          }),
      )
      .mockResolvedValue({ state: 'pending', connected: false });

    await renderAndStart();

    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);

    // Three ticks land while the first request is still out: they are skipped,
    // not queued, so one attempt never becomes four requests.
    await advance(POLL_MS * 3);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(1);

    // The slow answer arrives; the next tick is a new request again.
    await act(async () => {
      release?.({ state: 'waiting', connected: false });
    });
    expect(screen.getByTestId('channels-wechat-login-state')).toHaveTextContent(
      /已扫码，请在手机上确认|Scanned/,
    );
    await advance(POLL_MS);
    expect(api.pollWechatLoginState).toHaveBeenCalledTimes(2);
  });
});
