import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it } from 'vitest';
import { InspectorPanel } from './inspector-panel';

/**
 * The path in the preview header is a **breadcrumb**, so it should be antd's
 * `Breadcrumb` rather than a button, a `<span>/</span>` and a text node glued
 * together (task-37 #2). The assertions below are the ones that must survive the
 * swap: the root crumb navigates, the file name is the last crumb, and the
 * separator is structural (`ant-breadcrumb-separator`), not a text node we own.
 */
function renderPanel() {
  render(
    <MemoryRouter initialEntries={['/c/s-1/files/src/chat/composer.tsx']}>
      <Routes>
        <Route path="/c/:sessionId/files/*" element={<InspectorPanel />} />
        <Route path="/c/:sessionId/files" element={<InspectorPanel />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('inspector path breadcrumb', () => {
  it('renders antd Breadcrumb structure with the file as the last item', async () => {
    renderPanel();
    const crumb = await screen.findByTestId('inspector-breadcrumb');
    // The official component, not a hand-rolled div: antd owns the list, the
    // items and the separators.
    expect(crumb.className).toContain('ant-breadcrumb');
    const items = crumb.querySelectorAll('.ant-breadcrumb li');
    expect(items.length).toBeGreaterThanOrEqual(2);
    expect(items[items.length - 1].textContent).toContain('composer.tsx');
    // The separator is antd's own element, not a `<span>/</span>` we wrote.
    expect(crumb.querySelector('.ant-breadcrumb-separator')?.textContent).toBe('/');
  });

  it('navigates to the tree from the root crumb and keeps the panel mounted', async () => {
    const user = userEvent.setup();
    renderPanel();
    const root = await screen.findByRole('link', { name: '工作区根目录' });
    await user.click(root);
    expect(await screen.findByRole('complementary')).toBeInTheDocument();
    expect(screen.queryByTestId('inspector-breadcrumb')).toBeNull();
  });
});
