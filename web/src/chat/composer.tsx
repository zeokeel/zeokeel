import {
  useEffect,
  useRef,
  useState,
  type ComponentRef,
  type KeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ClipboardEvent as ReactClipboardEvent,
  type DragEvent as ReactDragEvent,
} from 'react';
import { Sender } from '@ant-design/x';
import { ArrowUpOutlined } from '@ant-design/icons';
import { Button, Tag, Typography } from 'antd';
import { formatBytes, type StagedAttachment } from '@/api/blobs';
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
  /**
   * `connecting` is the window between submitting and the **first frame** of any
   * kind (a tool call counts, not just the first token): in v0.3.0 a 30 s network
   * blackhole showed the idle hint the whole time, as if nothing had started.
   */
  phase?: 'idle' | 'connecting' | 'running';
  disabled?: boolean;
  placeholder?: string;
  /** Slash commands for this screen; omitted means no slash menu. */
  commands?: SlashCommand[];
  /**
   * The Sender's own slots (antd X `prefix`/`footer`), not a hand-rolled bar next
   * to it: the framework component owns the input's box, so controls that belong
   * to the input live inside it.
   */
  prefix?: React.ReactNode;
  footer?: React.ReactNode;
  /** Staged attachments (already uploaded); the screen owns them. */
  attachments?: StagedAttachment[];
  /** A file was picked or pasted; the screen uploads it. */
  onAttachFiles?: (files: File[]) => void;
  onRemoveAttachment?: (id: string) => void;
  /**
   * Why the composer is disabled, shown as a notice **above** the card.
   *
   * A disabled input explained only by a placeholder is an input whose reason
   * is invisible the moment the user starts looking for the cause: the text
   * sits at 1.8:1 contrast and disappears as soon as anything is typed. The
   * reason belongs next to the control, in a sentence the user can act on
   * (docs/audit/2026-11-24/FIELD-DESIGN.md §4).
   */
  notice?: string;
  /**
   * Bumped by the caller to ask for the caret ("fill the draft for me"). A
   * counter rather than a boolean so two clicks in a row both land.
   */
  focusRequest?: number;
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
/**
 * The composer's three states, derived from the run and from whether **any** frame
 * of the new turn has arrived yet.
 *
 * A tool call counts as a frame: waiting for the first *token* would leave the
 * hint saying "connecting" long after the run had visibly started.
 */
export interface ComposerFrame {
  role: 'user' | 'assistant';
  text?: string;
  reasoning?: string;
  steps?: unknown[];
}

/**
 * Takes the whole turn list, not "the last message": sending appends the user's
 * echo first, so a last-message reading marks a frame as arrived the instant the
 * composer is cleared and `connecting` never shows. Only the assistant frame of
 * the turn says whether the backend has produced anything yet.
 */
export function resolveComposerPhase(
  isRequesting: boolean,
  messages: readonly ComposerFrame[] = [],
): 'idle' | 'connecting' | 'running' {
  if (!isRequesting) return 'idle';
  let frame: ComposerFrame | undefined;
  for (let i = messages.length - 1; i >= 0; i -= 1) {
    if (messages[i]?.role === 'assistant') {
      frame = messages[i];
      break;
    }
  }
  const anyFrame =
    (frame?.text ?? '') !== '' ||
    (frame?.reasoning ?? '') !== '' ||
    (frame?.steps?.length ?? 0) > 0;
  return anyFrame ? 'running' : 'connecting';
}

export function Composer({
  value,
  onChange,
  onSubmit,
  onStop,
  running = false,
  phase,
  disabled = false,
  placeholder,
  commands,
  prefix,
  footer,
  attachments,
  onAttachFiles,
  onRemoveAttachment,
  notice,
  focusRequest,
}: ComposerProps) {
  const t = useT();
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const senderRef = useRef<ComponentRef<typeof Sender> | null>(null);
  const [highlight, setHighlight] = useState(0);
  const [dismissedQuery, setDismissedQuery] = useState<string | null>(null);
  const [filePaths, setFilePaths] = useState<string[] | null>(null);
  const [mentionHighlight, setMentionHighlight] = useState(0);
  const [dismissedMention, setDismissedMention] = useState<string | null>(null);

  const slashQuery = parseSlashDraft(value);
  const matches = slashQuery === null ? [] : filterSlashCommands(commands ?? [], slashQuery);
  // `phase` wins when given; the legacy `running` prop still maps to `running`.
  const resolvedPhase: 'idle' | 'connecting' | 'running' =
    phase ?? (running ? 'running' : 'idle');

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
    // A composing IME owns Enter and Escape: the candidate window's Enter picks
    // a candidate, and sending the raw pinyin instead is how a Chinese user
    // posts "nihao" mid-sentence (AUDIT round 2, F-5). 229 is the legacy
    // keyCode Firefox/Safari report for the same state.
    const native = event.nativeEvent as unknown as { isComposing?: boolean; keyCode?: number };
    if (native.isComposing || native.keyCode === 229) return undefined;

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

  const [dragging, setDragging] = useState(false);

  useEffect(() => {
    if (!focusRequest) return;
    senderRef.current?.focus?.();
  }, [focusRequest]);

  const attachFromList = (files: File[]) => {
    if (files.length === 0 || !onAttachFiles) return;
    onAttachFiles(files);
  };

  const handlePaste = (event: ReactClipboardEvent<HTMLDivElement>) => {
    // A blocked composer (approval pending) must not stage files either: the
    // attachments survived the approval and appeared afterwards (round-1
    // composer audit, checklist 7).
    if (disabled) return;
    const files = Array.from(event.clipboardData?.files ?? []);
    if (files.length === 0) return;
    // Prevent the browser from also pasting the image's markup/text.
    event.preventDefault();
    attachFromList(files);
  };

  const handleDragOver = (event: ReactDragEvent<HTMLDivElement>) => {
    if (disabled) return;
    if (!event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'copy';
    setDragging(true);
  };

  const handleDragLeave = (event: ReactDragEvent<HTMLDivElement>) => {
    // Leaving for a child element must not clear the overlay.
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
    setDragging(false);
  };

  const handleDrop = (event: ReactDragEvent<HTMLDivElement>) => {
    if (disabled) {
      setDragging(false);
      return;
    }
    const files = Array.from(event.dataTransfer?.files ?? []);
    setDragging(false);
    if (files.length === 0) return;
    event.preventDefault();
    attachFromList(files);
  };

  /**
   * A file dropped anywhere else would make the browser open it, replacing the
   * app with `file://…`. The window-level guard swallows those drops; the card's
   * own handler still receives the ones aimed at it.
   */
  useEffect(() => {
    const guard = (event: DragEvent) => {
      if (!event.dataTransfer?.types.includes('Files')) return;
      event.preventDefault();
      if (event.type === 'drop') setDragging(false);
    };
    // A cancelled drag (Esc) never sends `drop`; without these the overlay
    // would stay on screen until the next pointer move (R2-3).
    const clear = () => setDragging(false);
    const onLeaveWindow = (event: DragEvent) => {
      if (event.relatedTarget === null) clear();
    };
    window.addEventListener('dragover', guard);
    window.addEventListener('drop', guard);
    window.addEventListener('dragend', clear);
    window.addEventListener('dragleave', onLeaveWindow);
    return () => {
      window.removeEventListener('dragover', guard);
      window.removeEventListener('drop', guard);
      window.removeEventListener('dragend', clear);
      window.removeEventListener('dragleave', onLeaveWindow);
    };
  }, []);

  const empty = value.trim() === '';
  // An attachment that has not finished uploading cannot be sent yet.
  const attachmentsUploading = (attachments ?? []).some((item) => item.uploading);
  const hintKey =
    resolvedPhase === 'connecting'
      ? ('composer.hint.connecting' as const)
      : resolvedPhase === 'running'
        ? ('composer.hint.running' as const)
        : ('composer.hint.idle' as const);

  // Clicking the card's empty space returns the caret to the draft, the way
  // every desktop composer behaves. Clicks on a control are left alone.
  const focusInputOnBlank = (event: ReactMouseEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (
      target.closest(
        'button, a, input, textarea, .ant-select, [role="option"], [data-testid="composer-attachments"]',
      )
    ) {
      return;
    }
    event.preventDefault();
    senderRef.current?.focus?.();
  };

  const attach = onAttachFiles ? (
    <>
      <Button
        size="small"
        type="text"
        shape="circle"
        className="zk-composer__tool"
        aria-label={t('composer.attach')}
        title={t('composer.attach')}
        data-testid="composer-attach"
        onClick={() => fileInputRef.current?.click()}
      >
        <PlusIcon />
      </Button>
      <input
        ref={fileInputRef}
        type="file"
        multiple
        hidden
        data-testid="composer-file-input"
        accept="image/*,text/*,application/pdf,application/json"
        onChange={(event) => {
          const files = Array.from(event.target.files ?? []);
          event.target.value = '';
          if (files.length > 0) onAttachFiles(files);
        }}
      />
    </>
  ) : null;

  return (
    // `data-testid="composer"` marks the **input area boundary**, not the input:
    // the textarea is reached by role inside it (task-37 #1, decision (b)).
    <div
      className="zk-composer"
      data-testid="composer"
      data-disabled={disabled ? 'true' : 'false'}
      data-dragging={dragging ? 'true' : 'false'}
      onMouseDown={focusInputOnBlank}
      // Paste and drop are how people actually attach things: a screenshot goes
      // through the clipboard and a file through the window. Neither was wired,
      // so the only path was the paperclip (round-2 composer audit).
      onPaste={handlePaste}
      onDragOver={handleDragOver}
      onDragEnter={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      {dragging ? (
        <div className="zk-composer__drop" aria-hidden="true">
          {t('composer.dropHint')}
        </div>
      ) : null}
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

      {notice ? (
        <div className="zk-composer__notice" role="status" data-testid="composer-notice">
          <InfoIcon />
          <span>{notice}</span>
        </div>
      ) : null}

      {attachments?.some((item) => item.uploading) ? (
        // One line, once: the chips carry their own spinners, this says why the
        // send button is waiting.
        <div className="zk-composer__notice" role="status" data-testid="composer-uploading">
          <span>{t('composer.uploading')}</span>
        </div>
      ) : null}
      {attachments && attachments.length > 0 ? (
        <div className="zk-composer__attachments" data-testid="composer-attachments">
          {attachments.map((item) => (
            <Tag
              key={item.id}
              closable={!item.uploading}
              className={`zk-attach${item.uploading ? ' zk-attach--uploading' : ''}`}
              onClose={(event) => {
                event.preventDefault();
                onRemoveAttachment?.(item.id);
              }}
              data-testid="composer-attachment"
              data-uploading={item.uploading ? 'true' : undefined}
              aria-busy={item.uploading ? true : undefined}
            >
              {item.uploading ? <span className="zk-attach__spinner" aria-hidden="true" /> : null}
              {item.mime.startsWith('image/') && item.previewUrl ? (
                <img className="zk-attach__thumb" src={item.previewUrl} alt={item.name} />
              ) : null}
              <span className="zk-attach__text">
                <Typography.Text ellipsis>{item.name}</Typography.Text>
                <Typography.Text type="secondary">{formatBytes(item.bytes)}</Typography.Text>
              </span>
            </Tag>
          ))}
        </div>
      ) : null}

      <Sender
        ref={senderRef}
        value={value}
        onChange={handleChange}
        onSubmit={submit}
        onKeyDown={handleKeyDown}
        loading={running}
        onCancel={onStop}
        disabled={disabled}
        placeholder={placeholder ?? t('composer.placeholder')}
        // One line until the draft actually needs more: a two-line floor made an
        // empty composer 96px tall and pushed the buttons away from the text they
        // act on (round-2 F-14). 14 rows is DSH's cap, so the card scrolls before
        // it covers the transcript.
        autoSize={{ minRows: 1, maxRows: 14 }}
        submitType="enter"
        // The tool row lives **under** the draft, not beside it: controls in
        // `prefix` pushed the text column 250px to the right and made the
        // placeholder look like it floated in the middle (round-2 C-05/F-14).
        // Sender still owns the submit keymap (Enter) and the `submitDisabled`
        // bookkeeping the send button feeds; its own action stack stays mounted
        // but hidden (`.zk-composer__actions`), and the visible send / stop pair
        // lives in the toolbar with a name a screen reader can read
        // (round-2 F-12/C-11).
        footer={
          <div className="zk-composer__toolbar">
            <div className="zk-composer__tools">
              {attach}
              {prefix}
            </div>
            <div className="zk-composer__aside">
              {running ? (
                <Button
                  shape="circle"
                  className="zk-composer__stop"
                  icon={<StopIcon />}
                  onClick={onStop}
                  aria-label={t('composer.stop')}
                  title={t('composer.stop')}
                  data-testid="composer-stop"
                />
              ) : (
                <Button
                  type="primary"
                  shape="circle"
                  className="zk-composer__send"
                  icon={<ArrowUpOutlined />}
                  disabled={empty || disabled || attachmentsUploading}
                  onClick={submit}
                  aria-label={t('composer.send')}
                  title={t('composer.send')}
                  data-testid="composer-send"
                />
              )}
            </div>
          </div>
        }
        // Sender forwards aria-* to its textarea (X reads restProps through
        // pickAttrs); these props are not in SenderProps' type surface.
        {...{ 'aria-label': t('composer.inputLabel'), 'data-testid': 'composer-input' }}
      />
      {/* `data-phase` is the machine-readable form of the same state: localized
          copy is for people, the phase is for tests and for e2e, which must not
          match on a translated sentence. */}
      <div className="zk-composer__foot">
        {/* While the composer is blocked the notice already says why: keeping
            the key hints here would promise actions the disabled input cannot
            take (the old copy told the user to press Enter and Esc at once). */}
        <div
          className="zk-composer__hint"
          data-testid="composer-hint"
          data-phase={resolvedPhase}
          hidden={Boolean(notice)}
        >
          {/* The same moving indicator the thinking chain and the bubble use:
            * the running state had three different faces (round-4 component
            * audit #9). */}
          {resolvedPhase === 'running' || resolvedPhase === 'connecting' ? (
            <span className="zk-spinner" aria-hidden="true" />
          ) : null}
          {t(hintKey)}
          {commands && commands.length > 0 ? t('composer.hint.commands') : ''}
        </div>
        {footer ? <div className="zk-composer__meters">{footer}</div> : null}
      </div>
    </div>
  );
}

/** A dashed-outline plus, matching the 16px icon grid. */
function PlusIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false">
      <path
        d="M7 2.5v9M2.5 7h9"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        fill="none"
      />
    </svg>
  );
}

/** The stop glyph is a square, not a spinner: "loading" and "stop" are different. */
function StopIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false">
      <rect x="2" y="2" width="10" height="10" rx="2.5" fill="currentColor" />
    </svg>
  );
}

/** The disabled-reason notice's marker. */
function InfoIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false">
      <circle cx="7" cy="7" r="6" stroke="currentColor" strokeWidth="1.2" fill="none" />
      <path d="M7 6.2v4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <circle cx="7" cy="4.1" r="0.8" fill="currentColor" />
    </svg>
  );
}
