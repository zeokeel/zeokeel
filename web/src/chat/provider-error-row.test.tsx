import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '@/i18n';
import { AssistantBody } from './assistant-body';
import type { ZeoMessage } from './types';

/**
 * v0.3.1 ②: a provider failure must be actionable in the user's language, and the
 * original words must still be reachable — making the message nicer by dropping
 * the detail would make it less useful.
 */
const NO_KEY =
  'rig: provider "openai" stream: openai: chat completions failed: 401 Unauthorized: { "error": { "message": "You didn\'t provide an API key. …" } }';

beforeEach(() => {
  // jsdom reports en-US; this suite asserts the Chinese copy, so pin the browser
  // language the way a Chinese user's browser would report it.
  vi.stubGlobal('navigator', { ...navigator, language: 'zh-CN', languages: ['zh-CN'] });
});

function renderError(message: string) {
  const zeo: ZeoMessage = {
    role: 'assistant',
    text: '',
    reasoning: '',
    steps: [],
    stopReason: 'error',
    error: { code: '-32603', message },
  };
  render(
    <I18nProvider>
      <MemoryRouter initialEntries={['/c/s-1']}>
        <AssistantBody message={zeo} status="error" />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe('provider failure row', () => {
  it('says what to do, in Chinese, and offers the settings button', async () => {
    renderError(NO_KEY);
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('未配置模型或密钥无效');
    expect(within(alert).getByRole('button', { name: '打开设置' })).toBeInTheDocument();
  });

  it('keeps the original text, collapsed by default and expandable', async () => {
    renderError(NO_KEY);
    const alert = await screen.findByRole('alert');
    const details = alert.querySelector('details');
    expect(details, '原文必须是可展开的').not.toBeNull();
    expect(details?.hasAttribute('open')).toBe(false);

    const user = userEvent.setup();
    await user.click(within(alert).getByText(/详情/));
    expect(alert.textContent).toContain("You didn't provide an API key");
    // The internal code is not pushed at the user; it is part of the raw detail,
    // which the classifier strips from what we display as the report.
    expect(alert.textContent).not.toContain('（-32603）');
  });

  it('shows the raw text when the failure cannot be classified', async () => {
    renderError('weird internal failure');
    const alert = await screen.findByRole('alert');
    // Not swallowed: an unrecognised error is still the user's error.
    expect(alert.textContent).toContain('weird internal failure');
  });
});
