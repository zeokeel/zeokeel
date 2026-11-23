import { Button } from 'antd';
import { useNavigate } from 'react-router';
import { EmptyState } from '@/ui/empty-state';

/**
 * R25 · 团队 — the entry that must exist before the data does.
 *
 * C6 is a discoverability rule, not a data rule: the team/subagent entry is
 * always on screen, and zero state shows an empty surface ("nothing here yet"),
 * never a `0` count on the entry itself (docs/WEB-CLIENT.md §8.3 C6,
 * AGENT-SURFACES S9「零值常显」).
 *
 * The gateway exposes no `team.*` methods yet (TRANSPORT §5.2 declares six, the
 * gateway implements none), so this screen deliberately shows the empty state
 * rather than inventing rows: when the domain lands, the list fills in here and
 * the entry does not change shape.
 */
export function TeamScreen() {
  const navigate = useNavigate();

  return (
    <div className="zk-team">
      <EmptyState
        title="还没有队友或子任务"
        description="团队和子代理会在需要时出现在这里；现在没有正在协作的任务。"
      />
      <div className="zk-team__actions">
        <Button size="small" onClick={() => navigate('/')}>
          回到现在
        </Button>
      </div>
    </div>
  );
}