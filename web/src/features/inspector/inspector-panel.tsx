import { useEffect, useMemo, useState } from 'react';
import { Button, Segmented, Spin, Tree, Typography } from 'antd';
import type { TreeDataNode } from 'antd';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import {
  loadFileContent,
  loadFileDiff,
  loadFileTree,
  type FileNode,
} from '@/api/files';
import { DiffBlock } from '@/chat/tools/sections';
import { ZkIcon } from '@/design/icons';

/** Flat `a/b/c.ts` paths → antd Tree nodes, directories created as needed. */
export function buildFileTree(nodes: FileNode[]): TreeDataNode[] {
  type Dir = { children: Map<string, Dir>; node?: TreeDataNode };
  const root: Dir = { children: new Map() };

  for (const node of nodes) {
    const parts = node.path.split('/').filter(Boolean);
    let cursor = root;
    parts.forEach((part, index) => {
      const isLast = index === parts.length - 1;
      if (!cursor.children.has(part)) cursor.children.set(part, { children: new Map() });
      const next = cursor.children.get(part)!;
      if (isLast) {
        next.node = {
          key: node.path,
          title: part,
          isLeaf: node.isLeaf,
          ...(node.changed ? { icon: <span className="zk-tree-changed" aria-label="已改动" /> } : {}),
        };
      }
      cursor = next;
    });
  }

  const toTree = (dir: Dir): TreeDataNode[] =>
    [...dir.children.entries()].map(([name, child]) => {
      const children = toTree(child);
      if (child.node) {
        return { ...child.node, title: name, children: children.length > 0 ? children : undefined };
      }
      return { key: name, title: name, isLeaf: false, children };
    });

  return toTree(root);
}

/**
 * R8–R11 · the right inspector: the guide page and the file page (tree +
 * read-only preview + per-file diff).
 *
 * The inspector is a route group, not a floating panel (docs/WEB-CLIENT.md
 * §1.3), so a file is deep-linkable and a refresh restores it. History entries
 * are written only for a user's own navigation.
 */
export function InspectorPanel() {
  const { sessionId } = useParams<{ sessionId: string; '*': string }>();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const relative = useParams()['*'] ?? '';
  const [nodes, setNodes] = useState<FileNode[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [content, setContent] = useState<string | null>(null);
  const [diff, setDiff] = useState<string | null>(null);
  const [loadingFile, setLoadingFile] = useState(false);

  const view = searchParams.get('view') === 'diff' ? 'diff' : 'preview';
  const selected = relative || '';

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const tree = await loadFileTree();
        if (!cancelled) setNodes(tree.nodes);
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : '文件树加载失败');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    if (!selected) return;
    (async () => {
      try {
        setLoadingFile(true);
        if (view === 'diff') {
          const result = await loadFileDiff(selected);
          if (!cancelled) {
            setDiff(result.unified);
            setContent(null);
          }
        } else {
          const result = await loadFileContent(selected);
          if (!cancelled) {
            setContent(result.text);
            setDiff(null);
          }
        }
      } catch (cause) {
        if (!cancelled) setError(cause instanceof Error ? cause.message : '文件加载失败');
      } finally {
        if (!cancelled) setLoadingFile(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selected, view]);

  const treeData = useMemo(() => buildFileTree(nodes ?? []), [nodes]);
  const guide = relative === '' && !searchParams.get('view');

  return (
    <aside className="zk-inspector" aria-label="检查器">
      <div className="zk-inspector__tabs" role="tablist" aria-label="检查器页面">
        <button
          type="button"
          role="tab"
          aria-selected={guide}
          className={`zk-inspector__tab${guide ? ' zk-inspector__tab--active' : ''}`}
          onClick={() => sessionId && navigate(`/c/${sessionId}/guide`)}
        >
          开始
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={!guide}
          className={`zk-inspector__tab${!guide ? ' zk-inspector__tab--active' : ''}`}
          onClick={() => sessionId && navigate(`/c/${sessionId}/files`)}
        >
          文件
        </button>
      </div>

      {guide ? (
        <div className="zk-inspector__body">
          <Typography.Paragraph>
            检查器用来查看 agent 改了什么：左边是工作区树，右边是文件内容或逐文件 diff。
          </Typography.Paragraph>
          <Typography.Paragraph type="secondary">
            选择「文件」页开始；改动过的文件在树上有标记。
          </Typography.Paragraph>
        </div>
      ) : (
        <div className="zk-inspector__split">
          <div className="zk-inspector__tree">
            {error ? <div className="zk-msg-error">{error}</div> : null}
            {nodes === null ? (
              <Spin size="small" />
            ) : (
              <Tree
                treeData={treeData}
                virtual
                height={520}
                selectedKeys={selected ? [selected] : []}
                onSelect={(keys) => {
                  const next = String(keys[0] ?? '');
                  if (!next) return;
                  navigate(`/c/${sessionId}/files/${next}`);
                }}
              />
            )}
          </div>
          <div className="zk-inspector__preview">
            {selected === '' ? (
              <Typography.Text type="secondary">选择一个文件查看内容。</Typography.Text>
            ) : (
              <>
                <div className="zk-inspector__preview-header">
                  <ZkIcon name="file" />
                  <span className="zk-inspector__path">{selected}</span>
                  <Segmented
                    size="small"
                    value={view}
                    onChange={(value) => {
                      const params = new URLSearchParams(searchParams);
                      if (value === 'diff') params.set('view', 'diff');
                      else params.delete('view');
                      setSearchParams(params, { replace: true });
                    }}
                    options={[
                      { label: '预览', value: 'preview' },
                      { label: '改动', value: 'diff' },
                    ]}
                  />
                  <Button
                    size="small"
                    type="text"
                    onClick={() => sessionId && navigate(`/c/${sessionId}/files`)}
                  >
                    返回树
                  </Button>
                </div>
                {loadingFile ? (
                  <Spin size="small" />
                ) : view === 'diff' ? (
                  diff ? <DiffBlock unified={diff} /> : <Typography.Text type="secondary">没有改动。</Typography.Text>
                ) : content !== null ? (
                  <pre className="zk-inspector__content">{content}</pre>
                ) : (
                  <Typography.Text type="secondary">无法预览该文件。</Typography.Text>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </aside>
  );
}