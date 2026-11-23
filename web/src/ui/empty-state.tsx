import { Welcome } from '@ant-design/x';

/**
 * Empty state for the Now screen and a fresh thread. Deliberately no prompts,
 * no statistics, no cards: the only next action is the composer below.
 *
 * `title`/`description` stay plain strings — Welcome renders the title itself
 * inside `Typography.Title level={4}`, so passing our own heading would nest an
 * h4 in an h4.
 */
export function EmptyState({ title, description }: { title: string; description?: string }) {
  return (
    <div className="zk-empty">
      <Welcome variant="borderless" title={title} description={description} />
    </div>
  );
}