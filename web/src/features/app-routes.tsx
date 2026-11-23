import { Route, Routes, useLocation, useParams } from 'react-router';
import { Splitter } from 'antd';
import { AutomationScreen } from '@/features/automation/automation-screen';
import { CapabilitiesScreen } from '@/features/capabilities/capabilities-screen';
import { InboxScreen } from '@/features/inbox/inbox-screen';
import { InspectorPanel } from '@/features/inspector/inspector-panel';
import { InsightsScreen } from '@/features/insights/insights-screen';
import { TeamScreen } from '@/features/team/team-screen';
import { NowScreen } from '@/features/now/now-screen';
import { ROUTES } from '@/features/routes';
import { SettingsScreen } from '@/features/settings/settings-screen';
import { NotFoundScreen, StubScreen } from '@/features/stub/stub-screen';
import { ChildSessionScreen } from '@/features/thread/child-session-screen';
import { MemoryScreen } from '@/features/thread/memory-screen';
import { ThreadScreen } from '@/features/thread/thread-screen';
import { TrajectoryScreen } from '@/features/thread/trajectory-screen';

const meta = (path: string) => {
  const found = ROUTES.find((route) => route.path === path);
  if (!found) throw new Error(`no route metadata for ${path}`);
  return found;
};

/**
 * The thread column plus, for the file/guide routes, the right inspector.
 *
 * The inspector is a route group inside the same session (docs/WEB-CLIENT.md
 * §1.3): the thread stays visible, the file is deep-linkable, and only a user's
 * own file choice writes history.
 */
function ThreadRoute() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const location = useLocation();
  if (!sessionId) return <NotFoundScreen />;

  const inspector = /\/(files|guide)(\/|$)/.test(location.pathname);
  if (!inspector) return <ThreadScreen key={sessionId} sessionId={sessionId} />;

  return (
    <Splitter className="zk-thread-split">
      <Splitter.Panel min={360}>
        <ThreadScreen key={sessionId} sessionId={sessionId} />
      </Splitter.Panel>
      <Splitter.Panel defaultSize={380} min={280} max="55%">
        <InspectorPanel />
      </Splitter.Panel>
    </Splitter>
  );
}

/**
 * R1–R25. Every route in the contract is registered: built routes render their
 * screen, the rest render a placeholder that names the reference and the reason
 * (no convincing-looking empty screen).
 */
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<NowScreen />} />
      <Route path="/inbox" element={<InboxScreen />} />
      <Route path="/c/:sessionId" element={<ThreadRoute />} />
      <Route path="/c/:sessionId/trajectory" element={<TrajectoryScreen />} />
      <Route path="/c/:sessionId/memory" element={<MemoryScreen />} />
      <Route
        path="/c/:sessionId/context"
        element={<StubScreen meta={meta('/c/:sessionId/context')} />}
      />
      <Route path="/c/:sessionId/child/:childId" element={<ChildSessionScreen />} />
      <Route path="/c/:sessionId/files" element={<ThreadRoute />} />
      {/* Splat, not `*path`: react-router only accepts `*` as the splat segment,
          and a literal `*path` silently stops matching every file URL. */}
      <Route path="/c/:sessionId/files/*" element={<ThreadRoute />} />
      <Route path="/c/:sessionId/guide" element={<ThreadRoute />} />
      <Route path="/settings" element={<SettingsScreen />} />
      <Route path="/settings/:page" element={<SettingsScreen />} />
      <Route path="/automation" element={<AutomationScreen />} />
      <Route path="/capabilities" element={<CapabilitiesScreen />} />
      <Route path="/insights" element={<InsightsScreen />} />
      <Route path="/team" element={<TeamScreen />} />
      <Route path="*" element={<NotFoundScreen />} />
    </Routes>
  );
}