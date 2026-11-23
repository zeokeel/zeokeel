import type { ReactNode } from 'react';
import type { ToolStep } from '@/chat/types';
import {
  CodeBlock,
  DiffBlock,
  KeyValueList,
  MonoLine,
  SectionRow,
  TextBlock,
  asRecord,
  languageForPath,
  stringField,
  valueText,
} from './sections';

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
  target?: (args: Record<string, unknown>) => { label: string; text: string } | undefined;
  /** A unified diff the call produced or will produce ("将改动"). */
  changes?: (args: Record<string, unknown>, step: ToolStep) => string | undefined;
  /** Structured body: code, list, plan, output. */
  body?: (args: Record<string, unknown>, step: ToolStep) => ReactNode;
}

function defineCard(spec: CardSpec): CardRenderer {
  return (step: ToolStep) => {
    const args = asRecord(step.input);
    const target = spec.target?.(args);
    const changes = spec.changes?.(args, step);
    const body = spec.body?.(args, step);
    return (
      <>
        {target ? (
          <SectionRow label={target.label}>
            <MonoLine text={target.text} />
          </SectionRow>
        ) : null}
        {changes ? (
          <SectionRow label="将改动">
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
    <SectionRow label="输出">
      <TextBlock text={output} />
    </SectionRow>
  ) : null;
}

/** Output as highlighted code (shell output, file contents). */
function OutputCode({ step, lang, maxLines }: { step: ToolStep; lang: string; maxLines?: number }) {
  const output = valueText(step.output);
  return output ? (
    <SectionRow label="输出">
      <CodeBlock text={output} lang={lang} maxLines={maxLines} />
    </SectionRow>
  ) : null;
}

const patchOf = (args: Record<string, unknown>): string | undefined => {
  const patch = stringField(args, 'diff', 'patch', 'unified');
  return patch && patch.includes('@@') ? patch : undefined;
};

const pathTarget =
  (label = '路径') =>
  (args: Record<string, unknown>) => {
    const path = stringField(args, 'path', 'file_path', 'filename');
    return path ? { label, text: path } : undefined;
  };

const commandTarget =
  (label = '命令') =>
  (args: Record<string, unknown>) => {
    const command = stringField(args, 'command', 'cmd');
    return command ? { label, text: command } : undefined;
  };

const patternTarget =
  (label: string, ...keys: string[]) =>
  (args: Record<string, unknown>) => {
    const pattern = stringField(args, ...keys);
    return pattern ? { label, text: pattern } : undefined;
  };

const idTarget =
  (label: string, ...keys: string[]) =>
  (args: Record<string, unknown>) => {
    const id = stringField(args, ...keys);
    return id ? { label, text: id } : undefined;
  };

const languageOf = (args: Record<string, unknown>) => languageForPath(stringField(args, 'path'));

export const TOOL_RENDERERS: Record<string, CardRenderer> = {
  // ---------------------------------------------------------------- shell --
  bash: defineCard({
    target: commandTarget(),
    body: (_args, step) => <OutputCode step={step} lang="bash" maxLines={24} />,
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
        <SectionRow label="写入内容">
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
        <SectionRow label="替换">
          <KeyValueList
            entries={[
              ['原内容', before],
              ['新内容', after],
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
        <SectionRow label="替换">
          <KeyValueList
            entries={[
              ['原内容', before],
              ['新内容', after],
            ]}
          />
        </SectionRow>
      );
    },
  }),
  list_dir: defineCard({ target: pathTarget('目录'), body: (_args, step) => <OutputText step={step} /> }),
  glob: defineCard({
    target: patternTarget('模式', 'pattern', 'glob'),
    body: (_args, step) => <OutputText step={step} />,
  }),
  grep: defineCard({
    target: patternTarget('搜索', 'pattern', 'query', 'regex'),
    body: (args, step) => (
      <>
        <SectionRow label="范围">
          <MonoLine text={stringField(args, 'path', 'glob') ?? '工作区'} />
        </SectionRow>
        <OutputText step={step} />
      </>
    ),
  }),
  read_image: defineCard({
    target: pathTarget(),
    body: (_args, step) => (
      <SectionRow label="预览">
        <TextBlock
          text={step.error ?? '图片内容经 /blob/<id> 读取；服务端未提供预览数据时不伪造图像。'}
        />
      </SectionRow>
    ),
  }),

  // ------------------------------------------------------------------ web --
  web_fetch: defineCard({
    target: (args) => {
      const url = stringField(args, 'url');
      return url ? { label: '地址', text: url } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  web_search: defineCard({
    target: patternTarget('搜索', 'query', 'q'),
    body: (_args, step) => <OutputText step={step} />,
  }),

  // ------------------------------------------------------------ jobs/term --
  job_start: defineCard({
    target: commandTarget(),
    body: (_args, step) => <OutputCode step={step} lang="bash" maxLines={24} />,
  }),
  job_output: defineCard({
    target: idTarget('任务', 'jobId', 'job_id', 'id'),
    body: (_args, step) => <OutputText step={step} />,
  }),
  terminal_send: defineCard({
    target: (args) => {
      const text = stringField(args, 'input', 'text', 'data');
      return text ? { label: '输入', text } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  terminal_read: defineCard({ body: (_args, step) => <OutputText step={step} /> }),

  // ------------------------------------------------------------- worktree --
  enter_worktree: defineCard({
    target: (args) => {
      const path = stringField(args, 'path', 'worktree', 'branch');
      return path ? { label: '工作树', text: path } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  list_worktrees: defineCard({ body: (_args, step) => <OutputText step={step} /> }),

  // -------------------------------------------------------- orchestration --
  subagent: defineCard({
    target: (args) => {
      const task = stringField(args, 'description', 'prompt', 'task');
      return task ? { label: '任务', text: task } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  workflow: defineCard({
    target: (args) => {
      const name = stringField(args, 'name', 'workflow');
      return name ? { label: '工作流', text: name } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  ralph: defineCard({
    target: (args) => {
      const objective = stringField(args, 'objective', 'goal');
      return objective ? { label: '目标', text: objective } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  team_task_create: defineCard({
    target: (args) => {
      const subject = stringField(args, 'subject', 'title');
      return subject ? { label: '任务', text: subject } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  team_task_update: defineCard({
    target: idTarget('任务', 'taskId', 'task_id', 'id'),
    body: (_args, step) => <OutputText step={step} />,
  }),

  // -------------------------------------------------------- plan / todo ----
  todo: defineCard({ body: (_args, step) => <OutputText step={step} /> }),
  todo_write: defineCard({
    body: (args, step) => {
      const todos = Array.isArray(args.todos) ? args.todos : [];
      if (todos.length === 0) return <OutputText step={step} />;
      return (
        <SectionRow label="待办">
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
      return title ? { label: '计划', text: title } : undefined;
    },
    body: (args, step) => {
      const plan = stringField(args, 'plan', 'content');
      if (plan) {
        return (
          <SectionRow label="计划正文">
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
      return name ? { label: '技能', text: name } : undefined;
    },
    body: (_args, step) => <OutputText step={step} />,
  }),
  demo: defineCard({
    target: (args) => {
      const text = stringField(args, 'text', 'prompt', 'message');
      return text ? { label: '输入', text } : undefined;
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
      <SectionRow label="输入">
        <TextBlock text={input || '（无参数）'} />
      </SectionRow>
      {output ? (
        <SectionRow label="输出">
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