import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { KeyValueRows } from './sections';

/**
 * The key/value rows of a tool card are antd's `Descriptions` (task-37 #6), not a
 * hand-written `<dl>`: the component owns the label/value pairing, column layout
 * and a11y. Only the rows change — the tool card's own container stays ours
 * (it is one of the six surfaces antd X does not provide).
 */
describe('tool card key/value rows', () => {
  it('renders the label/value pairs through antd Descriptions', () => {
    render(
      <KeyValueRows
        rows={[
          { labelKey: 'tool.sect.output', text: 'ok' },
          { labelKey: 'thread.rawArguments', text: '{"url":"x"}' },
        ]}
      />,
    );

    const descriptions = document.querySelector('.ant-descriptions');
    expect(descriptions, 'antd Descriptions 的结构没有出现').not.toBeNull();

    // Labels and values are paired by the component, not by our markup.
    const labels = [...(descriptions?.querySelectorAll('.ant-descriptions-item-label') ?? [])];
    const values = [...(descriptions?.querySelectorAll('.ant-descriptions-item-content') ?? [])];
    expect(labels.map((node) => node.textContent)).toEqual(['输出', '原始参数']);
    expect(values.map((node) => node.textContent)).toEqual(['ok', '{"url":"x"}']);

    // The hand-written `<dl>` is gone — a regression would bring it back.
    expect(document.querySelector('dl.zk-kv')).toBeNull();
  });

  it('keeps every row on its own line (one column)', () => {
    render(
      <KeyValueRows
        rows={[
          { labelKey: 'tool.sect.output', text: 'a' },
          { labelKey: 'thread.rawArguments', text: 'b' },
        ]}
      />,
    );
    // Two rows, one column: each pair is its own item row rather than sharing one.
    const rowItems = document.querySelectorAll('.ant-descriptions-row');
    expect(rowItems.length).toBe(2);
    expect(screen.getByText('a')).toBeInTheDocument();
    expect(screen.getByText('b')).toBeInTheDocument();
  });
});
