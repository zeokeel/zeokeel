import type { ReactNode } from 'react';
import { Descriptions } from 'antd';
import { CodeHighlighter } from '@ant-design/x';
import { ghcolors, oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { parseUnifiedDiff, type DiffLine } from '@/api/files';
import { useDarkMode } from '@/chat/markdown';
import { useT } from '@/i18n';
import type { MessageKey } from '@/i18n';

/**
 * The section primitives every tool card is built from. One anatomy for all 26
 * cards (docs/AGENT-SURFACES.md S3): target → changes → output → raw arguments.
 * A renderer only chooses which sections apply; it never invents a new look.
 */

/**
 * One label/value pair of a tool card.
 *
 * A `dl` rather than two divs: a screen reader reads "地址, http://…" instead of
 * two unrelated strings, and the 13 section rows get the same pairing the 2
 * `Descriptions` uses had (round-4 component audit #4).
 */
export function SectionRow({ labelKey, children }: { labelKey: MessageKey; children: ReactNode }) {
  const t = useT();
  return (
    <dl className="zk-tool-section">
      <dt className="zk-tool-section__label">{t(labelKey)}</dt>
      <dd className="zk-tool-section__body">{children}</dd>
    </dl>
  );
}

/** Label/value rows whose labels come from the catalogue (keys, not strings). */
export function KeyValueRows({
  rows,
}: {
  rows: Array<{ labelKey: MessageKey; text: string }>;
}) {
  const t = useT();
  // antd owns the label/value pairing, the column layout and the ghcolors of the
  // pairing; the card around it stays ours (it is one of the six surfaces antd X
  // does not provide — docs/plans/antd-x-component-map.md §4).
  return (
    <Descriptions
      className="zk-kv"
      size="small"
      column={1}
      colon={false}
      items={rows.map((row) => ({
        key: row.labelKey,
        label: t(row.labelKey),
        children: row.text,
      }))}
    />
  );
}

/** A command / path / url target: mono, on the strong surface, one line each. */
export function MonoLine({ text }: { text: string }) {
  return <code className="zk-tool-mono">{text}</code>;
}

const LANGUAGE_BY_EXTENSION: Record<string, string> = {
  ts: 'ts',
  tsx: 'tsx',
  js: 'js',
  jsx: 'jsx',
  json: 'json',
  go: 'go',
  py: 'python',
  sh: 'bash',
  md: 'markdown',
  css: 'css',
  sql: 'sql',
  yml: 'yaml',
  yaml: 'yaml',
};

export function languageForPath(path: string | undefined): string {
  if (!path) return 'text';
  const ext = path.split('.').pop()?.toLowerCase() ?? '';
  return LANGUAGE_BY_EXTENSION[ext] ?? 'text';
}

export function CodeBlock({
  text,
  lang = 'text',
  maxLines,
}: {
  text: string;
  lang?: string;
  maxLines?: number;
}) {
  const t = useT();
  // The syntax theme has its own text colours: painting only the background with
  // a page token left dark-on-dark at 1.3:1 in dark mode (round-6 tools audit N4).
  const dark = useDarkMode();
  const lines = text.split('\n');
  const clipped = maxLines && lines.length > maxLines;
  const shown = clipped ? lines.slice(0, maxLines).join('\n') : text;
  return (
    <div className="zk-code">
      {/* A long output scrolls inside its card: slicing it left the rest of a
          200-line log unreachable (round-4 tools audit D9). */}
      <div className="zk-code__scroll">
        <CodeHighlighter
          lang={lang}
          header={false}
          // The syntax theme paints its own surface; the card belongs to the page,
          // and the theme's inline style beat the token in dark mode
          // (round-5 tools audit, dark 390: a white block on a black card).
          highlightProps={{
            style: dark ? oneDark : ghcolors,
            customStyle: {
              background: 'var(--zk-bg-strong)',
              fontFamily: 'var(--zk-font-mono)',
              margin: 0,
              borderRadius: 0,
            },
          }}
        >
          {shown}
        </CodeHighlighter>
      </div>
      {clipped ? (
        <div className="zk-tool-truncated">
          {t('thread.truncated', { max: maxLines, total: lines.length })}
        </div>
      ) : null}
    </div>
  );
}

/** Plain text output: no highlighter, no fake syntax. */
export function TextBlock({ text }: { text: string }) {
  return <pre className="zk-tool-io">{text}</pre>;
}

const DIFF_LABEL = { add: '+', del: '-', hunk: '', context: ' ', meta: '' } as const;

/**
 * Unified diff, verbatim from the gateway. The only colour is line tint plus a
 * `+`/`-` gutter, so add/delete never rely on colour alone.
 */
/** Old-file line number for every row, computed before rendering. */
export function numberDiffLines(lines: DiffLine[]) {
  let oldLine = 0;
  let newLine = 0;
  const rows: Array<{ kind: DiffLine['kind']; text: string; number: number | null }> = [];
  for (const line of lines) {
    let number: number | null = null;
    if (line.kind === 'hunk') {
      const match = line.text.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)/);
      if (match) {
        oldLine = Number(match[1]);
        newLine = Number(match[2]);
      }
    } else if (line.kind === 'context') {
      number = oldLine;
      oldLine += 1;
      newLine += 1;
    } else if (line.kind === 'del') {
      number = oldLine;
      oldLine += 1;
    } else if (line.kind === 'add') {
      number = newLine;
      newLine += 1;
    }
    rows.push({ kind: line.kind, text: line.text, number });
  }
  return rows;
}

export function DiffBlock({ unified }: { unified: string }) {
  const t = useT();
  const rows = numberDiffLines(parseUnifiedDiff(unified));

  return (
    <div className="zk-diff" role="group" aria-label={t('thread.fileDiff')}>
      {rows.map((row, index) => (
        <div key={index} className={`zk-diff__line zk-diff__line--${row.kind}`}>
          <span className="zk-diff__gutter">{row.number ?? ''}</span>
          <span className="zk-diff__sign">{DIFF_LABEL[row.kind]}</span>
          <span className="zk-diff__text">
            {/* Only content lines lose their marker; a meta line ("+++ b/app.ts")
                is shown whole, or it reads as a two-character prefix
                (AUDIT-2026-11-24 C). */}
            {row.kind === 'add' || row.kind === 'del' ? row.text.replace(/^[+-]/, '') : row.text}
          </span>
        </div>
      ))}
    </div>
  );
}

/** Any value as display text: strings verbatim, everything else as JSON. */
export function valueText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') {
    // Tool arguments arrive as JSON text. Printed verbatim, a Chinese payload reads
    // `\u7528\u6237…` and every quote is escaped; decoded, it reads like the JSON the
    // model actually wrote. Anything that is not JSON stays verbatim — a shell
    // command is not improved by quoting it.
    const text = value.trim();
    if (text.startsWith('{') || text.startsWith('[')) {
      try {
        return JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        return value;
      }
    }
    return value;
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/**
 * Arguments as a record.
 *
 * The live wire carries them as the JSON *string* the model produced (a settled
 * snapshot carries the parsed object), so a card that only accepted objects drew
 * no target section at all while the call was running — exactly when the user
 * needs to know what it is about to touch (round-4 tools audit D1).
 */
export function asRecord(value: unknown): Record<string, unknown> {
  if (typeof value === 'string') {
    const text = value.trim();
    if (!text) return {};
    try {
      const parsed: unknown = JSON.parse(text);
      return parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
        ? (parsed as Record<string, unknown>)
        : {};
    } catch {
      return {};
    }
  }
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function stringField(record: Record<string, unknown>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string' && value.trim() !== '') return value;
    if (Array.isArray(value) && value.every((item) => typeof item === 'string')) {
      return value.join(' ');
    }
  }
  return undefined;
}