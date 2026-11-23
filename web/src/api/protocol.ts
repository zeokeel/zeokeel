/**
 * Wire shapes for `POST /api/chat/run` (docs/TRANSPORT.md §4.6).
 *
 * The seven event types are the whole contract; anything else must degrade
 * (docs/WEB-CLIENT.md §3.2 C3) instead of throwing or blanking the transcript.
 */

export const RUN_EVENTS = [
  'message.delta',
  'reasoning.delta',
  'tool.start',
  'tool.result',
  'usage',
  'done',
  'error',
] as const;

export type RunEventName = (typeof RUN_EVENTS)[number];

export interface RunRequest {
  sessionId: string;
  /**
   * The turn's text. **Omitted** in regenerate mode: sending it would append a
   * second user prompt on top of the one the gateway already holds (§4.6).
   */
  text?: string;
  attachments?: string[];
  clientRunId: string;
  /**
   * Retry the last turn: the gateway rolls the tail back and reruns it, in one
   * request. There is deliberately no second send path (`message.regenerate`
   * was removed for exactly that reason).
   */
  regenerate?: boolean;
}

export interface MessageDeltaData {
  turnId: string;
  text: string;
}

export interface ReasoningDeltaData {
  turnId: string;
  text: string;
}

export interface ToolStartData {
  callId: string;
  name: string;
  input?: unknown;
}

export interface ToolResultData {
  callId: string;
  ok: boolean;
  output?: unknown;
  error?: string;
}

export interface UsageData {
  inputTokens?: number;
  outputTokens?: number;
  [key: string]: unknown;
}

export interface DoneData {
  turnId: string;
  stopReason?: string;
}

export interface ErrorData {
  code: string;
  message: string;
}

/** A parsed SSE event: the `event:` name plus its JSON `data:` payload. */
export interface RunEvent {
  name: RunEventName | 'unknown';
  rawName: string;
  data: unknown;
}

/**
 * Turn one `SSEOutput` chunk (`{ event, data }`, data still a raw string) into
 * a typed event. Malformed JSON, a missing name, or an unlisted name all come
 * back as `unknown` so the provider can record a diagnostic instead of
 * dropping the run.
 */
export function parseRunEvent(chunk: unknown): RunEvent | null {
  if (!chunk || typeof chunk !== 'object') return null;
  const { event, data } = chunk as { event?: unknown; data?: unknown };
  const rawName = typeof event === 'string' ? event : '';
  if (!rawName) return null;

  let parsed: unknown = data;
  if (typeof data === 'string') {
    try {
      parsed = JSON.parse(data);
    } catch {
      return { name: 'unknown', rawName, data: undefined };
    }
  }

  const known = (RUN_EVENTS as readonly string[]).includes(rawName);
  return { name: known ? (rawName as RunEventName) : 'unknown', rawName, data: parsed };
}