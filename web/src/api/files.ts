/** Workspace files, previews and diffs for the right inspector (R8–R11). */

export interface FileNode {
  key: string;
  path: string;
  title: string;
  isLeaf: boolean;
  /** The file has uncommitted changes; the row shows a change marker. */
  changed?: boolean;
}

export interface FileTree {
  root: string;
  nodes: FileNode[];
}

export interface FileContent {
  path: string;
  text: string;
  truncated: boolean;
}

export interface FileDiff {
  path: string;
  unified: string;
  additions: number;
  deletions: number;
}

async function readJson<T>(res: Response, what: string): Promise<T> {
  if (!res.ok) throw new Error(`${what} failed: ${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

export async function loadFileTree(): Promise<FileTree> {
  const res = await fetch('/api/files/tree', { headers: { Accept: 'application/json' } });
  return readJson<FileTree>(res, 'file tree');
}

export async function loadFileContent(path: string): Promise<FileContent> {
  const res = await fetch(`/api/files/content?path=${encodeURIComponent(path)}`, {
    headers: { Accept: 'application/json' },
  });
  return readJson<FileContent>(res, 'file content');
}

export async function loadFileDiff(path: string): Promise<FileDiff> {
  const res = await fetch(`/api/files/diff?path=${encodeURIComponent(path)}`, {
    headers: { Accept: 'application/json' },
  });
  return readJson<FileDiff>(res, 'file diff');
}

export interface DiffLine {
  kind: 'add' | 'del' | 'hunk' | 'context' | 'meta';
  text: string;
}

/**
 * Parse a unified diff into display lines. A hand-rolled parser is deliberate:
 * the client must render the gateway's diff verbatim (no re-derivation), and a
 * diff library would be a second formatting authority.
 */
export function parseUnifiedDiff(unified: string): DiffLine[] {
  const lines = unified.split('\n');
  // A unified diff ends with a newline; that final empty split is not a line.
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return lines.map((line) => {
    if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff ')) {
      return { kind: 'meta', text: line };
    }
    if (line.startsWith('@@')) return { kind: 'hunk', text: line };
    if (line.startsWith('+')) return { kind: 'add', text: line };
    if (line.startsWith('-')) return { kind: 'del', text: line };
    return { kind: 'context', text: line };
  });
}