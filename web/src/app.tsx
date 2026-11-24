import { useEffect } from 'react';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import enUS from 'antd/locale/en_US';
import { XProvider } from '@ant-design/x';
import { AppRoutes } from '@/features/app-routes';
import { ApprovalsProvider } from '@/features/approvals/approvals-context';
import { SessionsProvider } from '@/features/sessions/sessions-context';
import { KeyboardProvider } from '@/keyboard/keyboard-provider';
import { AppShell } from '@/shell/app-shell';
import { Toasts } from '@/features/toasts/toasts';
import { I18nProvider, useI18n } from '@/i18n';
import { zkThemeConfig } from '@/design/theme';
import { useSystemAppearance } from '@/design/use-system-appearance';
import { startGateway, stopGateway } from '@/api/gateway';

/**
 * Provider order (each one is installed exactly once):
 *   ConfigProvider  theme: algorithm + colorPrimary + fontFamily
 *   XProvider       the @ant-design/x locale/component context
 *   EscArbiter      the one document-capture Escape listener
 *   Sessions        the sidebar data
 *   Approvals       the approval queue + its Esc/Mod+Enter claims
 *   AppShell        the 4-zone chrome
 *
 * The router lives outside (main.tsx) so tests can mount the same tree in a
 * MemoryRouter.
 */
export function App() {
  const appearance = useSystemAppearance();

  // The CSS tokens and antd's algorithm must read the same value. The tokens
  // key off `html[data-theme]` (tokens.css emits one block per appearance), so
  // the resolved appearance is published here, once, before anything paints in
  // the new scheme. `color-scheme` rides along so form controls and the
  // scrollbar follow too (round-2 SYS-01: setting dark on a light OS used to
  // give white text on a white page).
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = appearance;
    root.style.colorScheme = appearance;
  }, [appearance]);

  // One control-plane connection for the whole app (§4.2). Providers below
  // subscribe through it; nothing else opens a socket.
  useEffect(() => {
    // Exchange `#token=` for the cookie first, then connect (§3.1). A stale
    // link is terminal: the badge shows the reopen hint instead of retrying.
    void startGateway();
    return () => stopGateway();
  }, []);

  return (
    <I18nProvider>
      <AppWithLocale appearance={appearance} />
    </I18nProvider>
  );
}

/** Locale-aware shell body: antd's own strings follow the same choice as ours. */
function AppWithLocale({ appearance }: { appearance: 'light' | 'dark' }) {
  const { locale } = useI18n();
  const antdLocale = locale === 'en' ? enUS : zhCN;

  return (
    <ConfigProvider theme={zkThemeConfig(appearance)} locale={antdLocale}>
      <XProvider>
        <KeyboardProvider>
          <SessionsProvider>
            <ApprovalsProvider>
              <Toasts>
                <AppShell>
                  <AppRoutes />
                </AppShell>
              </Toasts>
            </ApprovalsProvider>
          </SessionsProvider>
        </KeyboardProvider>
      </XProvider>
    </ConfigProvider>
  );
}