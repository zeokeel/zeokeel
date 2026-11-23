/**
 * The context timeline (`context:<sessionId>`, docs/TRANSPORT.md §5.1.9).
 *
 * Two rules shape this file:
 *
 *  1. **`context.delta` replaces the whole view.** The backend re-derives it
 *     from the durable event log every ~2s and only exposes a `revision`, so
 *     there is nothing to merge: the newest whole object wins.
 *  2. **`found:false` is an honest empty state.** A cold session has no foldable
 *     context yet; the UI says so instead of drawing a zero (and the gateway
 *     makes `sessionId` mandatory precisely so "client forgot the id" cannot
 *     masquerade as "this session has no context").
 */

export interface ContextSection {
  id: string;
  label?: string;
  source?: string;
  bytes: number;
  tokens: number;
  hash?: string;
}

export interface ContextTool {
  name: string;
  group?: string;
  tokens: number;
}

export interface ContextHistory {
  messages: number;
  user: number;
  assistant: number;
  tool: number;
}

export interface ContextInjected {
  agentsMd?: boolean;
  preset?: boolean;
  memory?: boolean;
  skills?: boolean;
}

export interface ContextSnapshotBlock {
  at?: string;
  seq?: number;
  model?: string;
  window?: number;
  systemPromptBytes: number;
  systemPromptTokens: number;
  sections?: ContextSection[];
  tools?: ContextTool[];
  history: ContextHistory;
  injected: ContextInjected;
  estimatedPrompt: number;
}

export interface ContextRequest {
  seq: number;
  iteration: number;
  at?: string;
  model?: string;
  prompt: number;
  completion: number;
  cached?: number;
  total: number;
  contextTokens: number;
  costUsd?: number;
  costKnown?: boolean;
}

export interface ContextEvent {
  seq: number;
  kind: string;
  at?: string;
  detail?: string;
  ref?: string;
  before?: number;
  after?: number;
  replaced?: number;
  saved?: number;
  path?: string;
  bytes?: number;
  inferred?: boolean;
  archivedMessages?: number;
  archivedBytes?: number;
  truncated?: boolean;
  outcome?: string;
}

export interface ContextView {
  revision: number;
  snapshot: ContextSnapshotBlock;
  requests: ContextRequest[];
  events: ContextEvent[];
  used: number;
  window: number;
  totalCostUsd?: number;
  costKnown?: boolean;
}

/** One session's context domain value: absent (`found:false`) or a full view. */
export interface ContextState {
  found: boolean;
  context: ContextView | null;
}

export interface ContextSnapshotResult {
  epoch: string;
  rev: number;
  sessionId: string;
  found: boolean;
  context?: ContextView;
}

export const EMPTY_CONTEXT: ContextState = { found: false, context: null };

export function normalizeContext(result: ContextSnapshotResult): ContextState {
  if (!result.found || !result.context) return EMPTY_CONTEXT;
  return { found: true, context: result.context };
}

/**
 * A delta is a **replacement**, never a patch: take `found`/`context` as given
 * and drop whatever was there. Merging fields would invent a view the backend
 * never produced.
 */
export function applyContextDelta(
  _previous: ContextState,
  delta: { found?: boolean; context?: ContextView | null },
): ContextState {
  if (!delta.found || !delta.context) return EMPTY_CONTEXT;
  return { found: true, context: delta.context };
}

/** Fraction of the window in use, 0 when the window is unknown (never a guess). */
export function windowUsage(view: ContextView | null): number | null {
  if (!view || !view.window || view.window <= 0) return null;
  return Math.min(1, Math.max(0, view.used / view.window));
}