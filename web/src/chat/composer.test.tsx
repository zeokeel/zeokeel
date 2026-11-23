import { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { Composer } from './composer';
import { EscArbiter } from '@/keyboard/esc-arbiter';
import { KeyboardProvider } from '@/keyboard/keyboard-provider';

function Harness({ onSubmit }: { onSubmit: () => void }) {
  const [value, setValue] = useState('');
  return (
    <KeyboardProvider esc={new EscArbiter()}>
      <Composer value={value} onChange={setValue} onSubmit={onSubmit} />
    </KeyboardProvider>
  );
}

const textbox = () => screen.getByRole('textbox');

describe('Composer keyboard contract', () => {
  it('sends on Enter', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    render(<Harness onSubmit={onSubmit} />);

    await user.click(textbox());
    await user.keyboard('hello{Enter}');
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('sends on Cmd/Ctrl+Enter without double-submitting', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    render(<Harness onSubmit={onSubmit} />);

    await user.click(textbox());
    await user.keyboard('hello');
    // Sender deliberately ignores a modified Enter; the composer handles it and
    // must consume the key so one press is one send.
    await user.keyboard('{Meta>}{Enter}{/Meta}');
    expect(onSubmit).toHaveBeenCalledTimes(1);

    await user.keyboard('{Control>}{Enter}{/Control}');
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });

  it('clears the draft on Escape through the single arbiter', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    render(<Harness onSubmit={onSubmit} />);

    await user.click(textbox());
    await user.keyboard('draft text');
    expect(textbox()).toHaveValue('draft text');

    await user.keyboard('{Escape}');
    expect(textbox()).toHaveValue('');
    // Clearing is not sending.
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('leaves Enter alone while the input is empty (nothing to submit)', async () => {
    const onSubmit = vi.fn();
    const user = userEvent.setup();
    render(<Harness onSubmit={onSubmit} />);

    await user.click(textbox());
    await user.keyboard('{Enter}');
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
describe('composer menus (A14–A17)', () => {
  it('opens the slash menu, closes it on Esc and keeps the draft', async () => {
    const user = userEvent.setup();
    const commands = [
      { name: 'new', description: '新建会话', run: vi.fn() },
      { name: 'clear', description: '清空草稿', run: vi.fn() },
    ];
    const onSubmit = vi.fn();
    function WithCommands() {
      const [value, setValue] = useState('');
      return (
        <KeyboardProvider esc={new EscArbiter()}>
          <Composer
            value={value}
            onChange={setValue}
            onSubmit={onSubmit}
            commands={commands}
          />
        </KeyboardProvider>
      );
    }
    render(<WithCommands />);
    const box = screen.getByRole('textbox');
    await user.click(box);
    await user.type(box, '/');
    expect(await screen.findByRole('listbox', { name: '斜杠命令' })).toBeInTheDocument();

    await user.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('listbox', { name: '斜杠命令' })).not.toBeInTheDocument(),
    );
    // A16: the draft is untouched.
    expect(box).toHaveValue('/');

    // Enter with the menu open runs the highlighted command instead of sending.
    await user.type(box, 'new');
    await user.keyboard('{Enter}');
    expect(commands[0]?.run).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('completes a slash command with Tab without executing it (K3)', async () => {
    const user = userEvent.setup();
    const run = vi.fn();
    function WithCommands() {
      const [value, setValue] = useState('');
      return (
        <KeyboardProvider esc={new EscArbiter()}>
          <Composer
            value={value}
            onChange={setValue}
            onSubmit={vi.fn()}
            commands={[{ name: 'settings', description: '打开设置', run }]}
          />
        </KeyboardProvider>
      );
    }
    render(<WithCommands />);
    const box = screen.getByRole('textbox');
    await user.click(box);
    await user.type(box, '/set');
    await user.keyboard('{Tab}');
    expect(box).toHaveValue('/settings ');
    expect(run).not.toHaveBeenCalled();
  });
});
