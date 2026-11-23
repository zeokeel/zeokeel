/** Mirrors x-sdk's MessageStatus enum (not re-exported from its package root). */
export type MessageStatus = 'local' | 'loading' | 'updating' | 'success' | 'error' | 'abort';

/** The message shape the transcript renders. One object per transcript row. */

export type ToolStepStatus = 'running' | 'ok' | 'error';

export interface ToolStep {
  callId: string;
  name: string;
  input?: unknown;
  output?: unknown;
  error?: string;
  status: ToolStepStatus;
}

export interface TokenUsage {
  inputTokens?: number;
  outputTokens?: number;
  [key: string]: unknown;
}

export interface MessageError {
  code: string;
  message: string;
}

export interface ZeoMessage {
  role: 'user' | 'assistant';
  text: string;
  reasoning: string;
  steps: ToolStep[];
  usage?: TokenUsage;
  /** `done` event's stopReason (e.g. `end_turn`, `aborted`). */
  stopReason?: string;
  /** A terminal `error` event. Rendered as a danger line, never as a warning. */
  error?: MessageError;
  /** Event names this client did not understand (C3 degrade, kept for diagnosis). */
  unknownEvents?: string[];
}

export function userMessage(text: string): ZeoMessage {
  return { role: 'user', text, reasoning: '', steps: [] };
}

export function assistantMessage(): ZeoMessage {
  return { role: 'assistant', text: '', reasoning: '', steps: [] };
}

/** One-line summary of a tool input, for the collapsed ThoughtChain node. */
export function toolSummary(input: unknown): string {
  if (input === undefined || input === null) return '';
  if (typeof input === 'string') return oneLine(input);
  if (typeof input === 'object') {
    const record = input as Record<string, unknown>;
    for (const key of ['path', 'command', 'query', 'pattern', 'url', 'name']) {
      const value = record[key];
      if (typeof value === 'string' && value.trim() !== '') return oneLine(value);
    }
    try {
      return oneLine(JSON.stringify(input));
    } catch {
      return '';
    }
  }
  return oneLine(String(input));
}

function oneLine(value: string): string {
  const flat = value.replace(/\s+/g, ' ').trim();
  return flat.length > 120 ? `${flat.slice(0, 117)}…` : flat;
}