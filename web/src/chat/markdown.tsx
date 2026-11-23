import type { ReactNode } from 'react';
import { XMarkdown } from '@ant-design/x-markdown';
import { CodeHighlighter } from '@ant-design/x';
import { nodesToText } from './nodes-to-text';

/** XMarkdown element overrides: one highlighter for the whole client. */
const markdownComponents = {
  // XMarkdown wraps fenced blocks in <pre><code>; CodeHighlighter brings its own
  // <pre>, so the wrapper degrades to a passthrough instead of nesting.
  pre: ({ children }: { children?: ReactNode }) => <>{children}</>,
  code: ({ block, lang, children }: { block?: boolean; lang?: string; children?: ReactNode }) => {
    if (!block) return <code className="zk-inline-code">{children}</code>;
    return (
      <CodeHighlighter lang={lang ?? 'text'} header={false}>
        {nodesToText(children)}
      </CodeHighlighter>
    );
  },
};

/**
 * Transcript body: one markdown renderer for the whole product —
 * `@ant-design/x-markdown`, which is streaming-aware and sanitises, so no
 * `react-markdown`/`marked` stack is added.
 */
export function MarkdownBody({ text, streaming }: { text: string; streaming: boolean }) {
  return (
    <div className="zk-markdown">
      <XMarkdown
        content={text}
        components={markdownComponents}
        // Typewriter is off: the transport already paces deltas (and the store
        // throttles them), so a second animation only adds lag. `hasNextChunk`
        // still tells the renderer that an unclosed fence is not final text.
        streaming={{ hasNextChunk: streaming, incremental: true }}
      />
    </div>
  );
}