import { useEffect, useState, type ReactNode } from 'react';
import { Drawer, Layout, Splitter } from 'antd';
import { matchPath, useLocation, useNavigate } from 'react-router';
import { useSessions } from '@/features/sessions/sessions-context';
import { useShortcut } from '@/keyboard/keyboard-provider';
import { useMediaQuery, useSystemAppearance } from '@/design/use-system-appearance';
import { CommandPalette } from '@/features/palette/command-palette';
import { Sidebar } from './sidebar';
import { TopBand } from './top-band';
import { describeError } from '@/features/describe-error';
import { useT } from '@/i18n';
import { subscribeSettings } from '@/api/gateway';
import { SETTING_KEYS } from '@/design/settings-keys';

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
  // 对话字号 (ui.chatFontSize) had no consumer either: the setting was stored and
  // ignored (AUDIT-2026-11-24 C). It lands on one CSS variable the transcript
  // reads, clamped to the readable range.
  useEffect(() => {
    const apply = (rows: Record<string, string>) => {
      const raw = Number.parseInt(rows[SETTING_KEYS.chatFontSize] ?? '', 10);
      const size = Number.isFinite(raw) ? Math.min(20, Math.max(12, raw)) : null;
      const root = document.documentElement;
      if (size === null) root.style.removeProperty('--zk-chat-font-size');
      else root.style.setProperty('--zk-chat-font-size', `${size}px`);
    };
    return subscribeSettings(apply);
  }, []);
  const [createError, setCreateError] = useState<string | null>(null);
  // B16: Mod+B folds the sidebar to an icon rail. The width is kept in a ref so
  // expanding restores the exact splitter size the user had (state not lost).
  const [collapsed, setCollapsed] = useState(false);
  // One structural breakpoint (docs/audit/2026-11-24/INTERACTION.md P0-2): below
  // it the sidebar cannot share the width with the content — 264 + 126 px left
  // the transcript one character wide. There it becomes a drawer the band opens.
  const narrow = useMediaQuery('(max-width: 1023px)');
  // The drawer is transient chrome, not part of the history entry: *any*
  // navigation closes it, including browser Back. Keying it on the pathname (two
  // rounds ago) or on the history key both re-opened it when Back returned to the
  // entry it was opened on (round-3 mobile audit F1). The state is adjusted
  // during render — React's documented "reset state when an input changes"
  // pattern — because a synchronous setState in an effect cascades renders.
  const [drawer, setDrawer] = useState<{
    openedFor: string | null;
    seenKey: string;
    seenNarrow: boolean;
  }>({ openedFor: null, seenKey: location.key, seenNarrow: narrow });
  // Both inputs reset the drawer: a new history entry, and a breakpoint flip.
  // Without the second one, widening the window (drawer unmounts) and narrowing
  // it again brought the drawer back by itself (round-3 mobile audit F5).
  if (drawer.seenKey !== location.key || drawer.seenNarrow !== narrow) {
    setDrawer({ openedFor: null, seenKey: location.key, seenNarrow: narrow });
  }
  const drawerOpen = narrow && drawer.openedFor !== null && drawer.openedFor === location.key;
  const openDrawer = () =>
    setDrawer({ openedFor: location.key, seenKey: location.key, seenNarrow: narrow });
  const closeDrawer = () => setDrawer((prev) => ({ ...prev, openedFor: null }));
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

  const sidebar = (
    <Sidebar
      activeKey={activeSessionId}
      collapsed={narrow ? false : collapsed}
      activeSessionId={activeSessionId}
      onSelect={(sessionId) => navigate(`/c/${sessionId}`)}
      onCreate={() => void handleCreate()}
    />
  );

  const column = (
    <div className="zk-column">
      <TopBand onOpenSidebar={narrow ? openDrawer : undefined} />
      <main id="zk-main" className="zk-content">
        {createError ? (
          <div className="zk-msg-error" role="alert">
            {createError}
          </div>
        ) : null}
        {children}
      </main>
    </div>
  );

  return (
    <div className="zk-shell" data-narrow={narrow ? 'true' : 'false'}>
      <CommandPalette />
      <a className="zk-skip-link" href="#zk-main">
        {t('shell.skipToContent')}
      </a>
      {narrow ? (
        <Layout className="zk-shell__body">
          <Drawer
            open={drawerOpen}
            placement="left"
            width={280}
            onClose={closeDrawer}
            className="zk-sidebar-drawer"
            styles={{ body: { padding: 0 } }}
            title={null}
            closable={false}
            data-testid="sidebar-drawer"
          >
            {sidebar}
          </Drawer>
          <div className="zk-column zk-column--narrow">{column}</div>
        </Layout>
      ) : (
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
              {sidebar}
            </Layout.Sider>
          </Splitter.Panel>
          <Splitter.Panel>
            {/* The band sits in the content column, beside the sidebar — the
                native frame's shape, and the order keyboard users tab through
                (sidebar → band → content, WEB-CLIENT §5.3). */}
            {column}
          </Splitter.Panel>
        </Splitter>
      </Layout>
      )}
    </div>
  );
}