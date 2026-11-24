import { useEffect, useMemo, useState } from 'react';
import { Breadcrumb, Segmented, Spin, Tree, Typography } from 'antd';
import type { TreeDataNode } from 'antd';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import {
  loadFileContent,
  loadFileDiff,
  loadFileTree,
  type FileNode,
} from '@/api/files';
import { DiffBlock } from '@/chat/tools/sections';
import { useT } from '@/i18n';

/** Flat `a/b/c.ts` paths → antd Tree nodes, directories created as needed. */
export function buildFileTree(nodes: FileNode[], changedLabel = ''): TreeDataNode[] {
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
          ...(node.changed
            ? { icon: <span className="zk-tree-changed" aria-label={changedLabel} /> }
            : {}),
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
  const t = useT();
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
        if (!cancelled) setError(cause instanceof Error ? cause.message : t('inspector.loadFailed'));
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
        if (!cancelled) setError(cause instanceof Error ? cause.message : t('inspector.fileLoadFailed'));
      } finally {
        if (!cancelled) setLoadingFile(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selected, view]);

  const treeData = useMemo(() => buildFileTree(nodes ?? [], t('inspector.changed')), [nodes, t]);
  const guide = relative === '' && !searchParams.get('view');

  return (
    <aside className="zk-inspector" aria-label={t('inspector.title')}>
      <div className="zk-inspector__tabs" role="tablist" aria-label={t('inspector.pages')}>
        <button
          type="button"
          role="tab"
          aria-selected={guide}
          className={`zk-inspector__tab${guide ? ' zk-inspector__tab--active' : ''}`}
          onClick={() => sessionId && navigate(`/c/${sessionId}/guide`)}
        >
          {t('inspector.guideTab')}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={!guide}
          className={`zk-inspector__tab${!guide ? ' zk-inspector__tab--active' : ''}`}
          onClick={() => sessionId && navigate(`/c/${sessionId}/files`)}
        >
          {t('inspector.filesTab')}
        </button>
      </div>

      {guide ? (
        <div className="zk-inspector__body">
          <Typography.Paragraph>
            {t('inspector.guide.title')}
          </Typography.Paragraph>
          <Typography.Paragraph type="secondary">
            {t('inspector.guide.hint')}
          </Typography.Paragraph>
        </div>
      ) : (
        <div className="zk-inspector__split">
          <div className="zk-inspector__tree">
            {error ? <div className="zk-msg-error">{error}</div> : null}
            {/* A failed load keeps `nodes` null, so the spinner used to spin
                forever next to the error (AUDIT-2026-11-24 C). */}
            {error ? null : nodes === null ? (
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
              <Typography.Text type="secondary">{t('inspector.pickFile')}</Typography.Text>
            ) : (
              <>
                <div className="zk-inspector__preview-header">
                  {/* C5 with antd's Breadcrumb (task-37 #2): the separators and
                      the list are the component's, not ours. The root crumb
                      navigates back to the tree and keeps this tab open — losing
                      the preview would lose the user's place. */}
                  <Breadcrumb
                    data-testid="inspector-breadcrumb"
                    className="zk-inspector__breadcrumb"
                    items={[
                      {
                        title: (
                          <a
                            href={`/c/${sessionId}/files`}
                            onClick={(event) => {
                              event.preventDefault();
                              navigate(`/c/${sessionId}/files`);
                            }}
                          >
                            {t('inspector.workspaceRoot')}
                          </a>
                        ),
                      },
                      { title: <span className="zk-inspector__path">{selected}</span> },
                    ]}
                  />
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
                      { label: t('inspector.preview'), value: 'preview' },
                      { label: t('inspector.diff'), value: 'diff' },
                    ]}
                  />
                </div>
                {loadingFile ? (
                  <Spin size="small" />
                ) : view === 'diff' ? (
                  diff ? <DiffBlock unified={diff} /> : <Typography.Text type="secondary">{t('inspector.noDiff')}</Typography.Text>
                ) : content !== null ? (
                  <pre className="zk-inspector__content">{content}</pre>
                ) : (
                  <Typography.Text type="secondary">{t('inspector.unpreviewable')}</Typography.Text>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </aside>
  );
}