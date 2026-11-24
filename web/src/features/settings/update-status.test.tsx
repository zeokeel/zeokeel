import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nProvider } from '@/i18n';

// The About page's update surface (docs/TRANSPORT.md §5.1.12). These tests pin
// the three behaviours that make it trustworthy: a failed check is shown and
// never rendered as "up to date", a staged update is visible, and installing is
// a separate verb from downloading.
const gateway = vi.hoisted(() => ({
  fetchUpdateStatus: vi.fn(),
  checkForUpdate: vi.fn(),
  downloadUpdate: vi.fn(),
  installUpdate: vi.fn(),
}));

vi.mock('@/api/gateway', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/gateway')>();
  return { ...actual, ...gateway };
});

import { UpdateStatus } from './update-status';

function renderPage() {
  return render(
    <I18nProvider>
      <UpdateStatus />
    </I18nProvider>,
  );
}

describe('UpdateStatus', () => {
  beforeEach(() => {
    Object.values(gateway).forEach((fn) => fn.mockReset());
  });

  it('shows the running version and an available release', async () => {
    gateway.fetchUpdateStatus.mockResolvedValue({
      currentVersion: '0.3.9',
      latestVersion: '0.4.0',
      available: true,
      notes: '## 新版本\n- 更快',
      checkedAt: '2026-11-24T10:00:00Z',
    });
    renderPage();
    expect(await screen.findByTestId('update-current')).toHaveTextContent('0.3.9');
    expect(await screen.findByTestId('update-latest')).toHaveTextContent('0.4.0');
    expect(screen.getByRole('button', { name: /下载更新|Download update/ })).toBeTruthy();
  });

  it('reports a failed check instead of claiming the product is current', async () => {
    gateway.fetchUpdateStatus.mockResolvedValue({
      currentVersion: '0.3.9',
      available: false,
      error: '连不上 GitHub',
    });
    renderPage();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('连不上 GitHub');
    // No "download" button: nothing is known to be available.
    expect(screen.queryByRole('button', { name: /下载更新|Download update/ })).toBeNull();
  });

  it('offers install only for a staged update, as a separate action', async () => {
    gateway.fetchUpdateStatus.mockResolvedValue({
      currentVersion: '0.3.9',
      latestVersion: '0.4.0',
      available: true,
      staged: { version: '0.4.0', sha256: 'abc', kind: 'appimage' },
    });
    gateway.installUpdate.mockResolvedValue({
      currentVersion: '0.3.9',
      latestVersion: '0.4.0',
      available: true,
      staged: { version: '0.4.0' },
    });
    renderPage();
    expect(await screen.findByTestId('update-staged')).toHaveTextContent('0.4.0');
    const install = await screen.findByRole('button', { name: /安装并重启|Install and restart/ });
    install.click();
    await vi.waitFor(() => expect(gateway.installUpdate).toHaveBeenCalledWith(true));
  });
});
