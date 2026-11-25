import { useSyncExternalStore, type ReactNode } from 'react';
import { XMarkdown } from '@ant-design/x-markdown';
import { CodeHighlighter } from '@ant-design/x';
import { darkCodeTheme, lightCodeTheme } from './syntax-theme';
import { nodesToText } from './nodes-to-text';

/** The live appearance, read from the one place the app publishes it. */
export function useDarkMode(): boolean {
  return useSyncExternalStore(
    (notify) => {
      const observer = new MutationObserver(notify);
      observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
      return () => observer.disconnect();
    },
    () => document.documentElement.dataset.theme !== 'light',
    () => false,
  );
}

/** XMarkdown element overrides: one highlighter for the whole client. */
function markdownComponents(dark: boolean) {
  return {
    // XMarkdown wraps fenced blocks in <pre><code>; CodeHighlighter brings its own
    // <pre>, so the wrapper degrades to a passthrough instead of nesting.
    pre: ({ children }: { children?: ReactNode }) => <>{children}</>,
    code: ({ block, lang, children }: { block?: boolean; lang?: string; children?: ReactNode }) => {
      if (!block) return <code className="zk-inline-code">{children}</code>;
      return (
        <CodeHighlighter
          lang={lang ?? 'text'}
          // The header is the language label plus the copy action; it used to be
          // suppressed (`header={false}`), which the round-4 reply audit read as a
          // code block with no language and no way to copy it. A fence with no
          // language has nothing to label, so its header is hidden rather than
          // showing a made-up "text".
          header={lang ? undefined : false}
          highlightProps={{
            // The stock palettes' tokens are inline styles that measured 2.5–3.7:1
            // on this product's surfaces; `syntax-theme` corrects every colour
            // against the surface it is drawn on, and a unit test measures them.
            style: dark ? darkCodeTheme() : lightCodeTheme(),
            // The library's own theme paints #fafafa and Fira Code; the block
            // belongs to the page's surface and mono stack instead.
            customStyle: {
              background: 'var(--zk-bg-strong)',
              fontFamily: 'var(--zk-font-mono)',
              margin: 0,
              padding: 0,
              border: 'none',
              borderRadius: 0,
              fontSize: 'var(--zk-type-xs)',
              lineHeight: 'var(--zk-type-xs-line)',
            },
          }}
          className="zk-code-block"
        >
          {nodesToText(children)}
        </CodeHighlighter>
      );
    },
    // A link in an answer must not navigate the app away: the transcript is a
    // document surface, so external links open in a new tab with a safe rel
    // (round-4 reply audit: clicking a link replaced the whole client).
    a: ({ href, children }: { href?: string; children?: ReactNode }) => (
      <a href={href} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ),
  };
}

/**
 * Transcript body: one markdown renderer for the whole product —
 * `@ant-design/x-markdown`, which is streaming-aware and sanitises, so no
 * `react-markdown`/`marked` stack is added.
 */
export function MarkdownBody({ text, streaming }: { text: string; streaming: boolean }) {
  const dark = useDarkMode();
  return (
    <div className="zk-markdown">
      <XMarkdown
        content={text}
        components={markdownComponents(dark)}
        // Typewriter is off: the transport already paces deltas (and the store
        // throttles them), so a second animation only adds lag. `hasNextChunk`
        // still tells the renderer that an unclosed fence is not final text.
        streaming={{ hasNextChunk: streaming, incremental: true }}
      />
    </div>
  );
}
