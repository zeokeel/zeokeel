import { isValidElement, type ReactNode } from 'react';

/**
 * Flat text of a React node tree. `CodeHighlighter` needs a string, while
 * XMarkdown hands its custom `code` component already-processed React nodes.
 */
export function nodesToText(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === 'boolean') return '';
  if (typeof node === 'string') return node;
  if (typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(nodesToText).join('');
  if (isValidElement<{ children?: ReactNode }>(node)) return nodesToText(node.props.children);
  return '';
}