import { useState, type ReactNode } from 'react';
import { Layout, Splitter } from 'antd';
import { matchPath, useLocation, useNavigate } from 'react-router';
import { useSessions } from '@/features/sessions/sessions-context';
import { useShortcut } from '@/keyboard/keyboard-provider';
import { useSystemAppearance } from '@/design/use-system-appearance';
import { CommandPalette } from '@/features/palette/command-palette';
import { Sidebar } from './sidebar';
import { TopBand } from './top-band';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';

const SIDEBAR_WIDTH_KEY = 'zk.sidebar.width';
const SIDEBAR_DEFAULT = 264;
/** Icon rail width when the sidebar is collapsed (Mod+B, B16). */
const COLLAPSED_WIDTH = 64;
/** Widths persist across reloads (S-02); the splitter owns the drag itself. */
function storedSidebarWidth(): number {
  if (typeof localStorage === 'undefined') return SIDEBAR_DEFAULT;
  const raw = localStorage.getItem(SIDEBAR_WIDTH_KEY);
  const parsed = raw ? Number(raw) : NaN;
  return Number.isFinite(parsed) && parsed >= 200 && parsed <= 380 ? parsed : SIDEBAR_DEFAULT;
}

/**
 * The 4-zone shell (S-01): sidebar, the content column with its band, and — on
 * the file routes — the right inspector (S-06). The inspector only exists while
 * a file route is open, so there is never an empty panel on screen: an empty
 * panel is not a placeholder, it is a bug the user can see.
 *
 * Splitter owns the draggable width; `Layout.Sider` owns the sidebar surface
 * (fill, edge rule, breakpoint collapse) inside the panel.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const t = useT();
  const location = useLocation();
  const navigate = useNavigate();
  const { create } = useSessions();
  // Layout.Sider defaults to its own dark surface; keep it aligned with the
  // appearance the ConfigProvider algorithm is using.
  const appearance = useSystemAppearance();
  const [createError, setCreateError] = useState<string | null>(null);
  // B16: Mod+B folds the sidebar to an icon rail. The width is kept in a ref so
  // expanding restores the exact splitter size the user had (state not lost).
  const [collapsed, setCollapsed] = useState(false);
  // The width the splitter had before folding, so expanding restores it instead
  // of jumping back to the default (B16: "展开后状态不丢").
  const [expandedWidth, setExpandedWidth] = useState(() => storedSidebarWidth());
  const activeSessionId = matchPath('/c/:sessionId/*', location.pathname)?.params.sessionId;

  const handleCreate = async () => {
    try {
      const session = await create();
      setCreateError(null);
      navigate(`/c/${session.id}`);
    } catch (cause) {
      setCreateError(describeError(cause, t, 'error.sessionCreateFailed'));
    }
  };

  useShortcut({
    id: 'toggle-sidebar',
    mod: true,
    key: 'b',
    active: true,
    run: () => {
      setCollapsed((was) => !was);
      return true;
    },
  });

  return (
    <div className="zk-shell">
      <CommandPalette />
      <a className="zk-skip-link" href="#zk-main">
        跳到主内容
      </a>
      <Layout className="zk-shell__body">
        <Splitter
          className="zk-split"
          onResizeEnd={(sizes) => {
            const width = sizes[0];
            if (width && typeof localStorage !== 'undefined') {
              localStorage.setItem(SIDEBAR_WIDTH_KEY, String(Math.round(width)));
              if (!collapsed) setExpandedWidth(Math.round(width));
            }
          }}
        >
          <Splitter.Panel
            defaultSize={collapsed ? COLLAPSED_WIDTH : expandedWidth}
            size={collapsed ? COLLAPSED_WIDTH : undefined}
            min={collapsed ? COLLAPSED_WIDTH : 200}
            max={collapsed ? COLLAPSED_WIDTH : 380}
            className="zk-shell__sidebar-panel"
          >
            <Layout.Sider
              width="100%"
              theme={appearance === 'dark' ? 'dark' : 'light'}
              className="zk-sidebar-sider"
            >
              <Sidebar
                activeKey={activeSessionId}
                collapsed={collapsed}
                activeSessionId={activeSessionId}
                onSelect={(sessionId) => navigate(`/c/${sessionId}`)}
                onCreate={() => void handleCreate()}
              />
            </Layout.Sider>
          </Splitter.Panel>
          <Splitter.Panel>
            {/* The band sits in the content column, beside the sidebar — the
                native frame's shape, and the order keyboard users tab through
                (sidebar → band → content, WEB-CLIENT §5.3). */}
            <div className="zk-column">
              <TopBand />
              <main id="zk-main" className="zk-content">
                {createError ? (
                  <div className="zk-msg-error" role="alert">
                    {createError}
                  </div>
                ) : null}
                {children}
              </main>
            </div>
          </Splitter.Panel>
        </Splitter>
      </Layout>
    </div>
  );
}