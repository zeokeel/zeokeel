import { useState, type KeyboardEvent } from 'react';
import { Sender } from '@ant-design/x';
import { loadFileTree } from '@/api/files';
import { useEscClaim } from '@/keyboard/keyboard-provider';
import { filterSlashCommands, parseSlashDraft, type SlashCommand } from './slash-commands';
import { useT } from '@/i18n';

/** The `@file` token the draft is currently typing, if any (A17). */
export function parseMentionDraft(value: string): string | null {
  const match = /(?:^|\s)@([^\s@]*)$/.exec(value);
  return match ? (match[1] ?? '') : null;
}

interface ComposerProps {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  onStop?: () => void;
  running?: boolean;
  disabled?: boolean;
  placeholder?: string;
  /** Slash commands for this screen; omitted means no slash menu. */
  commands?: SlashCommand[];
}

/**
 * The composer, one component for Now and Thread (the native UI does the same:
 * `session.composer` and `home.composer` are one surface, S7).
 *
 * Keyboard contract (docs/WEB-CLIENT.md §4.1, §5.2):
 *  - Enter sends (Sender's own `submitType="enter"`), Shift+Enter breaks a line;
 *  - Cmd/Ctrl+Enter also sends — Sender deliberately ignores a modified Enter;
 *  - Tab is never a submit: in the slash menu it only completes the text, and
 *    it is never intercepted elsewhere (focus navigation is a WCAG rule);
 *  - Esc reaches the arbiter only: it closes the slash menu (keeping the draft,
 *    A16) at the overlay level, and otherwise clears the draft (L6).
 */
export function Composer({
  value,
  onChange,
  onSubmit,
  onStop,
  running = false,
  disabled = false,
  placeholder,
  commands,
}: ComposerProps) {
  const t = useT();
  const [highlight, setHighlight] = useState(0);
  const [dismissedQuery, setDismissedQuery] = useState<string | null>(null);
  const [filePaths, setFilePaths] = useState<string[] | null>(null);
  const [mentionHighlight, setMentionHighlight] = useState(0);
  const [dismissedMention, setDismissedMention] = useState<string | null>(null);

  const slashQuery = parseSlashDraft(value);
  const matches = slashQuery === null ? [] : filterSlashCommands(commands ?? [], slashQuery);
  const menuOpen = slashQuery !== null && matches.length > 0 && dismissedQuery !== slashQuery;

  // @file candidates (A17): loaded once per composer, filtered as the user types.
  const mentionQuery = menuOpen ? null : parseMentionDraft(value);
  const mentionMatches =
    mentionQuery === null || filePaths === null
      ? []
      : filePaths
          .filter((path) => path.toLowerCase().includes(mentionQuery.toLowerCase()))
          .slice(0, 8);
  const mentionOpen =
    mentionQuery !== null &&
    mentionMatches.length > 0 &&
    dismissedMention !== mentionQuery;

  const handleChange = (next: string) => {
    if (parseMentionDraft(next) !== null && filePaths === null) {
      // Fetch lazily off the keystroke, not in an effect.
      void loadFileTree()
        .then((tree) => setFilePaths(tree.nodes.filter((node) => node.isLeaf).map((node) => node.path)))
        .catch(() => setFilePaths([]));
    }
    onChange(next);
  };

  const insertMention = (path: string) => {
    if (mentionQuery === null) return;
    const head = value.slice(0, value.length - mentionQuery.length - 1);
    onChange(`${head}@${path} `);
    setDismissedMention(null);
    setMentionHighlight(0);
  };

  const closeMenu = () => {
    if (slashQuery !== null) setDismissedQuery(slashQuery);
    setHighlight(0);
  };

  const runCommand = (command: SlashCommand | undefined) => {
    if (!command) return;
    closeMenu();
    if (!command.keepDraft) onChange('');
    void command.run();
  };

  useEscClaim({
    id: 'composer-slash-menu',
    level: 'overlay',
    active: menuOpen,
    onEscape: () => {
      // A16: the menu closes, the draft stays exactly as typed.
      closeMenu();
      return true;
    },
  });

  useEscClaim({
    id: 'composer-mention-menu',
    level: 'overlay',
    active: mentionOpen,
    onEscape: () => {
      if (mentionQuery !== null) setDismissedMention(mentionQuery);
      setMentionHighlight(0);
      return true;
    },
  });

  useEscClaim({
    id: 'composer-draft',
    level: 'clearDraft',
    active: value.trim() !== '',
    onEscape: () => {
      onChange('');
      return true;
    },
  });

  // An empty message is never a send, whichever path asked for it.
  const submit = () => {
    if (value.trim() === '') return;
    onSubmit();
  };

  const handleKeyDown = (event: KeyboardEvent) => {
    if (mentionOpen) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setMentionHighlight((index) => (index + 1) % mentionMatches.length);
        return false;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        setMentionHighlight((index) => (index - 1 + mentionMatches.length) % mentionMatches.length);
        return false;
      }
      if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        insertMention(mentionMatches[mentionHighlight]);
        return false;
      }
    }

    if (menuOpen) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setHighlight((index) => (index + 1) % matches.length);
        return false;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        setHighlight((index) => (index - 1 + matches.length) % matches.length);
        return false;
      }
      if (event.key === 'Enter') {
        event.preventDefault();
        runCommand(matches[highlight]);
        return false;
      }
      if (event.key === 'Tab') {
        // K3: Tab completes the text only — it executes nothing and does not
        // move focus (the completion is consumed here, so focus stays put).
        const command = matches[highlight];
        if (command) {
          event.preventDefault();
          onChange(`/${command.name} `);
          setDismissedQuery(null);
          setHighlight(0);
          return false;
        }
      }
    }

    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      submit();
      return false;
    }

    // While a run is active Sender's own send path is disabled (it turns the
    // button into 停止), yet Enter must still accept the prompt — into the
    // queue (A5/B13), never dropped. Shift+Enter keeps inserting a newline.
    if (event.key === 'Enter' && running && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      submit();
      return false;
    }
    return undefined;
  };

  return (
    <div className="zk-composer">
      {menuOpen ? (
        <div className="zk-slash" role="listbox" aria-label={t('composer.slash')}>
          {matches.map((command, index) => (
            <div
              key={command.name}
              role="option"
              aria-selected={index === highlight}
              className={`zk-slash__row${index === highlight ? ' zk-slash__row--active' : ''}`}
              onMouseEnter={() => setHighlight(index)}
              onMouseDown={(event) => {
                // Keep focus in the textarea: a click must not blur it first.
                event.preventDefault();
                runCommand(command);
              }}
            >
              <span className="zk-slash__name">/{command.name}</span>
              <span className="zk-slash__desc">{command.description}</span>
            </div>
          ))}
          <div className="zk-slash__hint">{t('composer.slashHint')}</div>
        </div>
      ) : null}

      {mentionOpen ? (
        <div className="zk-slash" role="listbox" aria-label={t('thread.fileCandidates')}>
          {mentionMatches.map((path, index) => (
            <div
              key={path}
              role="option"
              aria-selected={index === mentionHighlight}
              className={`zk-slash__row${index === mentionHighlight ? ' zk-slash__row--active' : ''}`}
              onMouseEnter={() => setMentionHighlight(index)}
              onMouseDown={(event) => {
                event.preventDefault();
                insertMention(path);
              }}
            >
              <span className="zk-slash__name">@{path}</span>
            </div>
          ))}
          <div className="zk-slash__hint">{t('composer.slashHint')}</div>
        </div>
      ) : null}

      <Sender
        value={value}
        onChange={handleChange}
        onSubmit={submit}
        onKeyDown={handleKeyDown}
        loading={running}
        onCancel={onStop}
        disabled={disabled}
        placeholder={placeholder ?? t('composer.placeholder')}
        autoSize={{ minRows: 2, maxRows: 6 }}
        submitType="enter"
        // Sender forwards aria-* to its textarea (X reads restProps through
        // pickAttrs); these props are not in SenderProps' type surface.
        {...{ 'aria-label': t('composer.inputLabel'), 'data-testid': 'composer-input' }}
      />
      <div className="zk-composer__hint" data-testid="composer-hint">
        {running
          ? t('composer.hint.running')
          : t('composer.hint.idle')}
        {commands && commands.length > 0 ? t('composer.hint.commands') : ''}
      </div>
    </div>
  );
}