import { useMemo, useState, type KeyboardEvent } from 'react';
import { Input, Modal } from 'antd';
import { useNavigate } from 'react-router';
import { useSessions } from '@/features/sessions/sessions-context';
import { useEscClaim, useShortcut } from '@/keyboard/keyboard-provider';
import { ZkIcon, type IconName } from '@/design/icons';

interface PaletteCommand {
  id: string;
  label: string;
  group: '操作' | '前往';
  icon: IconName;
  run: () => void | Promise<void>;
}

/**
 * S-07 the command palette: antd has none, so it is composed from `Modal` +
 * `Input` + a list — with its own keyboard handling (A18–A21).
 *
 * Contract: Mod+K opens and focuses the input immediately; ↑↓ move the
 * highlight; Enter runs it; Esc closes through the arbiter and clears the query
 * and the selection for the next opening. Mod+Shift+P is the alias for browsers
 * that swallow Mod+K (docs/WEB-CLIENT.md §4.2 rule 1).
 */
export function CommandPalette() {
  const navigate = useNavigate();
  const { create } = useSessions();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [activeIndex, setActiveIndex] = useState(0);

  const commands = useMemo<PaletteCommand[]>(
    () => [
      {
        id: 'new-session',
        label: '新建会话',
        group: '操作',
        icon: 'new-chat',
        run: async () => {
          const session = await create();
          navigate(`/c/${session.id}`);
        },
      },
      { id: 'go-now', label: '前往 现在', group: '前往', icon: 'home', run: () => navigate('/') },
      {
        id: 'go-inbox',
        label: '前往 收件箱',
        group: '前往',
        icon: 'message',
        run: () => navigate('/inbox'),
      },
      {
        id: 'go-settings',
        label: '前往 设置',
        group: '前往',
        icon: 'sliders',
        run: () => navigate('/settings/model'),
      },
      {
        id: 'go-capabilities',
        label: '前往 能力',
        group: '前往',
        icon: 'plugin',
        run: () => navigate('/capabilities'),
      },
      {
        id: 'go-automation',
        label: '前往 自动化',
        group: '前往',
        icon: 'clock',
        run: () => navigate('/automation'),
      },
      {
        id: 'go-insights',
        label: '前往 洞察',
        group: '前往',
        icon: 'activity',
        run: () => navigate('/insights'),
      },
    ],
    [create, navigate],
  );

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return commands;
    return commands.filter((command) => command.label.toLowerCase().includes(needle));
  }, [commands, query]);

  const close = () => {
    setOpen(false);
    // A21: the next opening starts clean.
    setQuery('');
    setActiveIndex(0);
  };

  const openPalette = () => {
    setQuery('');
    setActiveIndex(0);
    setOpen(true);
    return true;
  };

  useShortcut({ id: 'palette-open', mod: true, key: 'k', active: !open, run: openPalette });
  useShortcut({
    id: 'palette-open-alias',
    mod: true,
    shift: true,
    key: 'p',
    active: !open,
    run: openPalette,
  });

  useEscClaim({
    id: 'command-palette',
    level: 'overlay',
    active: open,
    onEscape: () => {
      close();
      return true;
    },
  });

  const runCommand = async (command: PaletteCommand | undefined) => {
    if (!command) return;
    close();
    await command.run();
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActiveIndex((index) => (filtered.length === 0 ? 0 : (index + 1) % filtered.length));
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActiveIndex((index) =>
        filtered.length === 0 ? 0 : (index - 1 + filtered.length) % filtered.length,
      );
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      void runCommand(filtered[activeIndex]);
    }
  };

  return (
    <Modal
      open={open}
      onCancel={close}
      footer={null}
      title="命令面板"
      width={560}
      destroyOnHidden
      // antd's own Esc handling would race the arbiter; the arbiter owns Esc.
      keyboard={false}
      mask={{ closable: true }}
    >
      <Input
        autoFocus
        value={query}
        onChange={(event) => {
          setQuery(event.target.value);
          setActiveIndex(0);
        }}
        onKeyDown={onKeyDown}
        placeholder="输入以过滤命令…"
        aria-label="命令搜索"
        aria-activedescendant={
          filtered[activeIndex] ? `zk-palette-${filtered[activeIndex].id}` : undefined
        }
      />
      <ul className="zk-palette" role="listbox" aria-label="命令">
        {filtered.length === 0 ? (
          <li className="zk-palette__empty">没有匹配的命令</li>
        ) : (
          filtered.map((command, index) => (
            <li
              key={command.id}
              id={`zk-palette-${command.id}`}
              role="option"
              aria-selected={index === activeIndex}
              className={`zk-palette__row${index === activeIndex ? ' zk-palette__row--active' : ''}`}
              onMouseEnter={() => setActiveIndex(index)}
              onClick={() => void runCommand(command)}
            >
              <ZkIcon name={command.icon} />
              <span>{command.label}</span>
              <span className="zk-palette__group">{command.group}</span>
            </li>
          ))
        )}
      </ul>
    </Modal>
  );
}