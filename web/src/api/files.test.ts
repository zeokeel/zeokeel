import { describe, expect, it } from 'vitest';
import { parseUnifiedDiff } from './files';
import { numberDiffLines } from '@/chat/tools/sections';
import { buildFileTree } from '@/features/inspector/inspector-panel';

const UNIFIED = `--- a/src/a.ts
+++ b/src/a.ts
@@ -1,3 +1,4 @@
 const a = 1;
-const b = 2;
+const b = 3;
+const c = 4;
 const d = 5;
`;

describe('parseUnifiedDiff', () => {
  it('classifies every line of a unified diff', () => {
    expect(parseUnifiedDiff(UNIFIED).map((line) => line.kind)).toEqual([
      'meta',
      'meta',
      'hunk',
      'context',
      'del',
      'add',
      'add',
      'context',
    ]);
  });

  it('numbers rows from the hunk header: old line for context/del, new line for add', () => {
    const rows = numberDiffLines(parseUnifiedDiff(UNIFIED));
    const numbered = rows.filter((row) => row.number !== null);
    expect(numbered.map((row) => row.number)).toEqual([1, 2, 2, 3, 3]);
    expect(
      rows
        .filter((row) => row.kind === 'add')
        .map((row) => row.text),
    ).toEqual(['+const b = 3;', '+const c = 4;']);
  });
});

describe('buildFileTree', () => {
  it('nests flat paths and keeps the changed marker on the leaf', () => {
    const tree = buildFileTree([
      { key: 'src', path: 'src', title: 'src', isLeaf: false },
      { key: 'src/api', path: 'src/api', title: 'api', isLeaf: false },
      { key: 'src/api/a.ts', path: 'src/api/a.ts', title: 'a.ts', isLeaf: true, changed: true },
      { key: 'README.md', path: 'README.md', title: 'README.md', isLeaf: true },
    ]);

    const src = tree.find((node) => node.title === 'src');
    expect(src?.children).toHaveLength(1);
    const api = src?.children?.[0];
    expect(api?.title).toBe('api');
    const leaf = api?.children?.[0];
    expect(leaf?.key).toBe('src/api/a.ts');
    expect(leaf?.isLeaf).toBe(true);
    expect(leaf?.icon).toBeTruthy();
    expect(tree.find((node) => node.title === 'README.md')?.isLeaf).toBe(true);
  });
});
