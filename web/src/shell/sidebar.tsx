import { Button } from 'antd';
import { Conversations } from '@ant-design/x';
import { useSessions } from '@/features/sessions/sessions-context';
import { SidebarNav } from './sidebar-nav';

/**
 * The session list. `Conversations` owns the list semantics (selection, item
 * roles, the creation row); this component only supplies data and the
 * navigation side effect, so the client keeps no second list implementation.
 */
export function Sidebar({
  activeKey,
  onSelect,
  onCreate,
}: {
  activeKey?: string;
  onSelect: (sessionId: string) => void;
  onCreate: () => void;
}) {
  const { sessions, loading, error, reload } = useSessions();

  const items = sessions.map((session) => ({
    key: session.id,
    label: session.title || '未命名会话',
  }));

  return (
    <div className="zk-sidebar">
      <div className="zk-sidebar__header">
        <span className="zk-sidebar__brand">ZeoKeel</span>
        {/* Text, not filled: 新建会话 is on every screen and must not hold an
            Accent fill (rule 10: ≤2 fills / ≤1 primary per screen). */}
        <Button size="small" type="text" onClick={onCreate} aria-label="新建会话">
          新建会话
        </Button>
      </div>
      <div className="zk-sidebar__nav">
        <SidebarNav />
      </div>
      <div className="zk-sidebar__list">
        <Conversations
          items={items}
          activeKey={activeKey}
          onActiveChange={(key) => onSelect(String(key))}
        />
        {loading ? <div className="zk-sidebar__empty">正在加载…</div> : null}
        {error ? (
          <div className="zk-sidebar__empty">
            <span>会话列表加载失败：{error}</span>
            <div>
              <Button size="small" type="link" onClick={() => void reload()}>
                重试
              </Button>
            </div>
          </div>
        ) : null}
        {!loading && !error && sessions.length === 0 ? (
          <div className="zk-sidebar__empty">还没有会话。在右侧输入一句话即可开始。</div>
        ) : null}
      </div>
    </div>
  );
}