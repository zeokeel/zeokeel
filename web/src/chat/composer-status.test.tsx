import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ComposerFooter, ComposerPickers, thinkingSettingKey } from '@/chat/composer-status';
import { SETTING_KEYS } from '@/design/settings-keys';

/**
 * The composer's small components (native parity): the permission preset, the model,
 * the thinking effort, the prompt queue and the usage line. Each picker writes the
 * settings row the backend reads on every turn, so the assertion is on the write.
 */

const MODELS = {
  defaultModel: 'deepseek-v4',
  models: [
    {
      id: 'deepseek-v4',
      name: 'DeepSeek V4',
      thinkingModes: [
        { id: 'default', nameZh: '默认', nameEN: 'Default' },
        { id: 'reasoning-high', nameZh: '高强度', nameEN: 'High' },
      ],
    },
    { id: 'qwen3', name: 'Qwen 3', thinkingModes: [{ id: 'default', nameZh: '默认', nameEN: 'Default' }] },
  ],
};

function stubFetch(models: unknown = MODELS) {
  vi.stubGlobal('fetch', async (url: string) => {
    if (String(url).includes('/api/models')) {
      return new Response(JSON.stringify(models), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response('not found', { status: 404 });
  });
}

const writes: Array<[string, string]> = [];

const baseProps = {
  sessionId: 's-1',
  permission: 'workspace',
  model: 'deepseek-v4',
  thinkingMode: 'default',
  running: false,
  onSetting: (key: string, value: string) => writes.push([key, value]),
  onInterject: () => undefined,
};

afterEach(() => {
  vi.unstubAllGlobals();
  writes.length = 0;
});

describe('composer status', () => {
  it('shows the permission preset and writes it back', async () => {
    stubFetch();
    const user = userEvent.setup();
    render(<ComposerPickers {...baseProps} />);

    const picker = await screen.findByTestId('composer-permission');
    expect(picker.textContent).toContain('工作区内修改');
    await user.click(picker);
    await user.click(await screen.findByTitle('只读'));
    await waitFor(() => expect(writes).toContainEqual([SETTING_KEYS.agentPermission, 'read-only']));
  });

  it('lists the catalog models and writes chat.model', async () => {
    stubFetch();
    const user = userEvent.setup();
    render(<ComposerPickers {...baseProps} />);

    const picker = await screen.findByTestId('composer-model');
    await waitFor(() => expect(picker.textContent).toContain('DeepSeek V4'));
    await user.click(picker);
    await user.click(await screen.findByTitle('Qwen 3'));
    await waitFor(() => expect(writes).toContainEqual([SETTING_KEYS.chatModel, 'qwen3']));
  });

  it('writes the per-session thinking mode when the model offers choices', async () => {
    stubFetch();
    const user = userEvent.setup();
    render(<ComposerPickers {...baseProps} />);

    const picker = await screen.findByTestId('composer-thinking');
    await user.click(picker);
    await user.click(await screen.findByTitle('高强度'));
    await waitFor(() =>
      expect(writes).toContainEqual([thinkingSettingKey('s-1', 'deepseek-v4'), 'reasoning-high']),
    );
  });

  it('hides the thinking picker when the model has a single mode', async () => {
    stubFetch(MODELS);
    render(<ComposerPickers {...baseProps} model="qwen3" />);
    await screen.findByTestId('composer-model');
    await waitFor(() => expect(screen.queryByTestId('composer-thinking')).toBeNull());
  });

  /**
   * The role problem (2026-11-26 audit, 高): a session whose selected role no
   * longer resolves has NO tools and every call is refused. The composer is where
   * the conversation is, so it is where the reason has to appear — and only then:
   * a healthy session must not gain a warning strip.
   */
  it('shows the unresolvable role, and nothing at all when the role is fine', async () => {
    stubFetch();
    const { rerender } = render(
      <ComposerPickers {...baseProps} presetId="ghost" presetProblem="unknown-preset" />,
    );
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('ghost');
    expect(alert.textContent).toContain('工具调用会被拒绝');

    // A resolvable role, and a session with no role at all: no warning.
    rerender(<ComposerPickers {...baseProps} presetId="lead" />);
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
    rerender(<ComposerPickers {...baseProps} />);
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
  });

  it('renders the usage line from the context numbers', async () => {
    stubFetch();
    render(
      <ComposerFooter
        {...baseProps}
        usage={{ turns: 3, steps: 7, tokens: 12956, cached: 9000, prompt: 10000, context: 12877, window: 131072 }}
      />,
    );
    const line = await screen.findByTestId('composer-usage');
    expect(line.textContent).toContain('3 轮 7 步');
    expect(line.textContent).toContain('13.0k tok');
    expect(line.textContent).toContain('缓存 90%');
    expect(line.textContent).toContain('上下文 10%');
    expect(within(line).getByText(/13\.0k/)).toBeInTheDocument();
  });
});
