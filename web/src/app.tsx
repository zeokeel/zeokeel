import { useEffect } from 'react';
import { ConfigProvider } from 'antd';
import { XProvider } from '@ant-design/x';
import { AppRoutes } from '@/features/app-routes';
import { ApprovalsProvider } from '@/features/approvals/approvals-context';
import { SessionsProvider } from '@/features/sessions/sessions-context';
import { KeyboardProvider } from '@/keyboard/keyboard-provider';
import { AppShell } from '@/shell/app-shell';
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

  // One control-plane connection for the whole app (§4.2). Providers below
  // subscribe through it; nothing else opens a socket.
  useEffect(() => {
    // Exchange `#token=` for the cookie first, then connect (§3.1). A stale
    // link is terminal: the badge shows the reopen hint instead of retrying.
    void startGateway();
    return () => stopGateway();
  }, []);

  return (
    <ConfigProvider theme={zkThemeConfig(appearance)}>
      <XProvider>
        <KeyboardProvider>
          <SessionsProvider>
            <ApprovalsProvider>
              <AppShell>
                <AppRoutes />
              </AppShell>
            </ApprovalsProvider>
          </SessionsProvider>
        </KeyboardProvider>
      </XProvider>
    </ConfigProvider>
  );
}