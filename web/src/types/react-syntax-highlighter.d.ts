/**
 * `react-syntax-highlighter` ships no types for its theme modules and the project
 * does not take `@types/*` for it. The only use is handing a Prism theme object
 * to `CodeHighlighter.highlightProps`.
 */
declare module 'react-syntax-highlighter/dist/esm/styles/prism' {
  export const oneLight: Record<string, unknown>;
  export const ghcolors: Record<string, unknown>;
  export const oneDark: Record<string, unknown>;
}
