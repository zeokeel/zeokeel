/** Slash commands offered by the composer (A14–A16, K3/K13). */

export interface SlashCommand {
  /** Without the leading slash. */
  name: string;
  description: string;
  /** Run on Enter/click. */
  run: () => void | Promise<void>;
  /** Keep the draft after running (default: clear it). */
  keepDraft?: boolean;
}

/** The `/query` a draft is currently typing, or null when it is not a slash draft. */
export function parseSlashDraft(value: string): string | null {
  if (!value.startsWith('/')) return null;
  const rest = value.slice(1);
  if (/\s/.test(rest)) return null;
  return rest;
}

export function filterSlashCommands(commands: SlashCommand[], query: string): SlashCommand[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return commands;
  return commands.filter(
    (command) =>
      command.name.toLowerCase().startsWith(needle) ||
      command.description.toLowerCase().includes(needle),
  );
}