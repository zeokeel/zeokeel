import type { ReactNode } from 'react';
import type { ToolStep } from '@/chat/types';
import type { MessageKey } from '@/i18n';
import { useT } from '@/i18n';
import {
  CodeBlock,
  DiffBlock,
  KeyValueRows,
  MonoLine,
  SectionRow,
  TextBlock,
  asRecord,
  languageForPath,
  stringField,
  valueText,
} from './sections';

/** Raw arguments, with a translated stand-in when the call carried none. */
function ArgsBlock({ input }: { input: string }) {
  const t = useT();
  return <TextBlock text={input || t('tool.sect.noArgs')} />;
}

/** Falls back to a translated word instead of a raw empty string. */
function ScopeLine({ path }: { path: string | undefined }) {
  const t = useT();
  return <MonoLine text={path ?? t('tool.sect.workspace')} />;
}

/** The image card says why it shows no picture rather than faking one. */
function ImageNote({ error }: { error: string | undefined }) {
  const t = useT();
  return <TextBlock text={error ?? t('tool.sect.imageNote')} />;
}

/**
 * The 26 tool-card renderers and their dispatch table
 * (docs/AGENT-SURFACES.md S3; mirrors internal/ui/toolcards/toolcards.go).
 *
 * Every card is built from the same sections — target → changes → output — so a
 * tool gets a card for *what it does*, never for how it looks. An unregistered
 * tool name (MCP, a future tool) falls back to `GenericToolCard`, which still
 * shows the raw arguments and the output: degrade, never blank.
 */
export interface ToolRendererProps {
  step: ToolStep;
}

/**
 * A renderer is a plain function returning nodes, not a component: the client
 * picks it by tool name at render time, and a component identifier chosen in
 * render is exactly the pattern React's compiler lint forbids.
 */
type CardRenderer = (step: ToolStep) => ReactNode;

interface CardSpec {
  /** The thing being acted on: command, path, url, pattern. */
  target?: (args: Record<string, unknown>) => { labelKey: MessageKey; text: string } | undefined;
  /** A unified diff the call produced or will produce ("将改动"). */
  changes?: (args: Record<string, unknown>, step: ToolStep) => string | undefined;
  /** Structured body: code, list, plan, output. */
  body?: (args: Record<string, unknown>, step: ToolStep) => ReactNode;
}

function defineCard(spec: CardSpec): CardRenderer {
  // `t` is unused by most cards (their copy is a `labelKey` on a component), so
  // it is accepted and ignored here rather than threaded through every body.
  return (step: ToolStep) => {
    const args = asRecord(step.input);
    const target = spec.target?.(args);
    const changes = spec.changes?.(args, step);
    const body = spec.body?.(args, step);
    return (
      <>
        {target ? (
          <SectionRow labelKey={target.labelKey}>
            <MonoLine text={target.text} />
          </SectionRow>
        ) : null}
        {changes ? (
          <SectionRow labelKey="tool.sect.changes">
            <DiffBlock unified={changes} />
          </SectionRow>
        ) : null}
        {body}
      </>
    );
  };
}

/** Output as plain text, in the shared "输出" section. */
function OutputText({ step }: { step: ToolStep }) {
  const output = valueText(step.output);
  return output ? (
    <SectionRow labelKey="tool.sect.output">
      <TextBlock text={output} />
    </SectionRow>
  ) : null;
}

/** Output as highlighted code (shell output, file contents). */
function OutputCode({ step, lang, maxLines }: { step: ToolStep; lang: string; maxLines?: number }) {
  const output = valueText(step.output);
  return output ? (
    <SectionRow labelKey="tool.sect.output">
      <CodeBlock text={output} lang={lang} maxLines={maxLines} />
    </SectionRow>
  ) : null;
}

const patchOf = (args: Record<string, unknown>): string | undefined => {
  const patch = stringField(args, 'diff', 'patch', 'unified');
  return patch && patch.includes('@@') ? patch : undefined;
};

const pathTarget =
  (labelKey: MessageKey = 'tool.target.path') =>
  (args: Record<string, unknown>) => {
    const path = stringField(args, 'path', 'file_path', 'filename');
    return path ? { labelKey, text: path } : undefined;
  };

const commandTarget =
  (labelKey: MessageKey = 'tool.target.command') =>
  (args: Record<string, unknown>) => {
    const command = stringField(args, 'command', 'cmd');
    return command ? { labelKey, text: command } : undefined;
  };

const patternTarget =
  (labelKey: MessageKey, ...keys: string[]) =>
  (args: Record<string, unknown>) => {
    const pattern = stringField(args, ...keys);
    return pattern ? { labelKey, text: pattern } : undefined;
  };

const idTarget =
  (labelKey: MessageKey, ...keys: string[]) =>
  (args: Record<string, unknown>) => {
    const id = stringField(args, ...keys);
    return id ? { labelKey, text: id } : undefined;
  };

const languageOf = (args: Record<string, unknown>) => languageForPath(stringField(args, 'path'));

export const TOOL_RENDERERS: Record<string, CardRenderer> = {
  // ---------------------------------------------------------------- shell --
  bash: defineCard({
    target: commandTarget(),
    // No line cap: the block scrolls inside the card, and a cap made the rest of
    // a 200-line output unreachable (round-5 tools audit D9).
    body: (_args, step) => <OutputCode step={step} lang="bash" />,
  }),

  // ----------------------------------------------------------------- file --
  read_file: defineCard({
    target: pathTarget(),
    body: (args, step) => <OutputCode step={step} lang={languageOf(args)} maxLines={32} />,
  }),
  write_file: defineCard({
    target: pathTarget(),
    changes: (args) => patchOf(args),
    body: (args, step) => {
      if (patchOf(args)) return null;
      const written = stringField(args, 'content', 'text');
      return written ? (
        <SectionRow labelKey="tool.sect.writeContent">
          <CodeBlock text={written} lang={languageOf(args)} maxLines={32} />
        </SectionRow>
      ) : (
        <OutputText step={step} />
      );
    },
  }),
  edit_file: defineCard({
    target: pathTarget(),
    changes: (args) => patchOf(args),
    body: (args, step) => {
      const before = stringField(args, 'old_string', 'old');
      const after = stringField(args, 'new_string', 'new');
      if (!before || !after || patchOf(args)) return <OutputText step={step} />;
      return (
        <SectionRow labelKey="tool.sect.replace">
          <KeyValueRows
            rows={[
              { labelKey: 'tool.sect.oldContent', text: before },
              { labelKey: 'tool.sect.newContent', text: after },
            ]}
          />
        </SectionRow>
      );
    },
  }),
  str_replace_editor: defineCard({
    target: pathTarget(),
    changes: (args) => patchOf(args),
    body: (args, step) => {
      const before = stringField(args, 'old_str', 'old_string');
      const after = stringField(args, 'new_str', 'new_string');
      if (!before || !after) return <OutputText step={step} />;
      return (
        <SectionRow labelKey="tool.sect.replace">
          <KeyValueRows
            rows={[
              { labelKey: 'tool.sect.oldContent', text: before },
              { labelKey: 'tool.sect.newContent', text: after },
            ]}
          />
        </SectionRow>
      );
    },
  }),
  list_dir: defineCard({ target: pathTarget('tool.target.dir'), body: (_args, step) => <OutputText step={step} /> }),
  glob: defineCard({
    target: patternTarget('tool.target.pattern', 'pattern', 'glob'),
    body: (_args, step) => <OutputText step={step} />,
  }),
  grep: defineCard({
    target: patternTarget('tool.target.search', 'pattern', 'query', 'regex'),
    body: (args, step) => (
      <>
        <SectionRow labelKey="tool.sect.scope">
          <ScopeLine path={stringField(args, 'path', 'glob')} />
        </SectionRow>
        <OutputText step={step} />
      </>
    ),
  }),
  read_image: defineCard({
    target: pathTarget(),
    body: (_args, step) => (
      <SectionRow labelKey="tool.sect.preview">
        <ImageNote error={step.error} />
      </SectionRow>
    ),
  }),

  // ------------------------------------------------------------------ web --
  web_fetch: defineCard({
    target: (args) => {
      const url = stringField(args, 'url');
      return url ? { labelKey: 'tool.target.url', text: url } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  web_search: defineCard({
    target: patternTarget('tool.target.search', 'query', 'q'),
    body: (_args, step) => <OutputText step={step} />,
  }),

  // ------------------------------------------------------------ jobs/term --
  job_start: defineCard({
    target: commandTarget(),
    // No line cap: the block scrolls inside the card, and a cap made the rest of
    // a 200-line output unreachable (round-5 tools audit D9).
    body: (_args, step) => <OutputCode step={step} lang="bash" />,
  }),
  job_output: defineCard({
    target: idTarget('tool.target.task', 'jobId', 'job_id', 'id'),
    body: (_args, step) => <OutputText step={step} />,
  }),
  terminal_send: defineCard({
    target: (args) => {
      const text = stringField(args, 'input', 'text', 'data');
      return text ? { labelKey: 'tool.sect.input', text } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  terminal_read: defineCard({ body: (_args, step) => <OutputText step={step} /> }),

  // ------------------------------------------------------------- worktree --
  enter_worktree: defineCard({
    target: (args) => {
      const path = stringField(args, 'path', 'worktree', 'branch');
      return path ? { labelKey: 'tool.target.worktree', text: path } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  list_worktrees: defineCard({ body: (_args, step) => <OutputText step={step} /> }),

  // -------------------------------------------------------- orchestration --
  subagent: defineCard({
    target: (args) => {
      const task = stringField(args, 'description', 'prompt', 'task');
      return task ? { labelKey: 'tool.target.task', text: task } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  workflow: defineCard({
    target: (args) => {
      const name = stringField(args, 'name', 'workflow');
      return name ? { labelKey: 'tool.target.workflow', text: name } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  ralph: defineCard({
    target: (args) => {
      const objective = stringField(args, 'objective', 'goal');
      return objective ? { labelKey: 'tool.target.objective', text: objective } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  team_task_create: defineCard({
    target: (args) => {
      const subject = stringField(args, 'subject', 'title');
      return subject ? { labelKey: 'tool.target.task', text: subject } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  team_task_update: defineCard({
    target: idTarget('tool.target.task', 'taskId', 'task_id', 'id'),
    body: (_args, step) => <OutputText step={step} />,
  }),

  // -------------------------------------------------------- plan / todo ----
  todo: defineCard({ body: (_args, step) => <OutputText step={step} /> }),
  todo_write: defineCard({
    body: (args, step) => {
      const todos = Array.isArray(args.todos) ? args.todos : [];
      if (todos.length === 0) return <OutputText step={step} />;
      return (
        <SectionRow labelKey="tool.sect.todos">
          <ul className="zk-tool-list">
            {todos.map((item, index) => {
              const record = asRecord(item);
              const text = stringField(record, 'content', 'text', 'title') ?? valueText(item);
              const status = stringField(record, 'status') ?? 'pending';
              return (
                <li key={index} className={`zk-tool-list__item zk-tool-list__item--${status}`}>
                  {text}
                </li>
              );
            })}
          </ul>
        </SectionRow>
      );
    },
  }),
  plan_review: defineCard({
    target: (args) => {
      const title = stringField(args, 'title', 'summary');
      return title ? { labelKey: 'tool.target.plan', text: title } : undefined;
    },
    body: (args, step) => {
      const plan = stringField(args, 'plan', 'content');
      if (plan) {
        return (
          <SectionRow labelKey="tool.sect.planBody">
            <CodeBlock text={plan} lang="markdown" maxLines={24} />
          </SectionRow>
        );
      }
      return <OutputText step={step} />;
    },
  }),
  skill: defineCard({
    target: (args) => {
      const name = stringField(args, 'name', 'skill');
      return name ? { labelKey: 'tool.target.skill', text: name } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  demo: defineCard({
    target: (args) => {
      const text = stringField(args, 'text', 'prompt', 'message');
      return text ? { labelKey: 'tool.sect.input', text } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
};

/**
 * The fallback card. It keeps the same anatomy, so an unknown tool still shows
 * what it was asked to do and what came back — plus the protocol name, which is
 * the only clue a user has for an MCP tool.
 */
export function GenericToolCard(step: ToolStep): ReactNode {
  const input = valueText(step.input);
  const output = valueText(step.output);
  return (
    <>
      <SectionRow labelKey="tool.sect.input">
        <ArgsBlock input={input} />
      </SectionRow>
      {output ? (
        <SectionRow labelKey="tool.sect.output">
          <TextBlock text={output} />
        </SectionRow>
      ) : null}
    </>
  );
}

export function toolRenderer(name: string): CardRenderer {
  return TOOL_RENDERERS[name] ?? GenericToolCard;
}

/** Render one tool's card body. */
export function renderToolCard(step: ToolStep): ReactNode {
  return toolRenderer(step.name)(step);
}

export const RENDERER_NAMES = Object.keys(TOOL_RENDERERS);