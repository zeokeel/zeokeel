import { Route, Routes, useLocation, useNavigate, useParams } from 'react-router';
import { Splitter } from 'antd';
import { AutomationScreen } from '@/features/automation/automation-screen';
import { CapabilitiesScreen } from '@/features/capabilities/capabilities-screen';
import { InboxScreen } from '@/features/inbox/inbox-screen';
// Two inboxes, two routes: `/inbox` is the approvals aggregation (R2), while
// `/inbox/incoming` is the gateway's inbound work queue (R2b,
// docs/TRANSPORT.md §5.1.13). Aliased so the two never get confused here.
import { InboxScreen as WorkInboxScreen } from '@/features/inbox/InboxScreen';
import { InspectorPanel } from '@/features/inspector/inspector-panel';
import { useMediaQuery } from '@/design/use-system-appearance';
import { InsightsScreen } from '@/features/insights/insights-screen';
import { TaskDetailScreen } from '@/features/task/TaskDetailScreen';
import { TeamScreen } from '@/features/team/team-screen';
import { ContextScreen } from '@/features/thread/context-screen';
import { NowScreen } from '@/features/now/now-screen';
import { SettingsScreen } from '@/features/settings/settings-screen';
import { NotFoundScreen } from '@/features/not-found-screen';
import { ChildSessionScreen } from '@/features/thread/child-session-screen';
import { MemoryScreen } from '@/features/thread/memory-screen';
import { ThreadScreen } from '@/features/thread/thread-screen';
import { TrajectoryScreen } from '@/features/thread/trajectory-screen';

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
  const navigate = useNavigate();
  // Two 360/280 panels do not fit a phone: at 390 the conversation column was
  // 10px wide and the composer 0px (round-1 tails audit F3). Below 900 the
  // inspector takes the whole width and owns the way back.
  const narrow = useMediaQuery('(max-width: 899px)');
  if (!sessionId) return <NotFoundScreen />;

  const inspectorRoute = /\/(files|guide)(\/|$)/.test(location.pathname);
  if (!inspectorRoute) return <ThreadScreen key={sessionId} sessionId={sessionId} />;

  if (narrow) {
    return (
      <div className="zk-thread-split zk-thread-split--narrow">
        <InspectorPanel onBack={() => navigate(`/c/${sessionId}`)} />
      </div>
    );
  }

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
      <Route path="/inbox/incoming" element={<WorkInboxScreen />} />
      <Route path="/c/:sessionId" element={<ThreadRoute />} />
      <Route path="/c/:sessionId/trajectory" element={<TrajectoryScreen />} />
      <Route path="/c/:sessionId/memory" element={<MemoryScreen />} />
      <Route
        path="/c/:sessionId/context"
        element={<ContextScreen />}
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
      <Route path="/t/:taskId" element={<TaskDetailScreen />} />
      <Route path="*" element={<NotFoundScreen />} />
    </Routes>
  );
}