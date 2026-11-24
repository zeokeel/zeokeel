/** Read models for the remaining screens: capabilities (R23), automation (R22), insights (R24). */

import { authedFetch } from './http';

export interface SkillSummary {
  id: string;
  name: string;
  description: string;
  source: string;
}

export interface ConnectorSummary {
  id: string;
  name: string;
  transport: string;
  tools: number;
  /** Localized sentence, kept for display next to the state dot. */
  status: string;
  /**
   * What the client switches on. The gateway derives it from the toolkit's
   * sentence so no component has to compare localized strings (round-2 C-04).
   */
  state?: 'connected' | 'failed' | 'idle';
  /** The provider's failure reason when `state === 'failed'`. */
  error?: string;
}

export interface Capabilities {
  skills: SkillSummary[];
  connectors: ConnectorSummary[];
  /** Connector names the toolkit is skipping (workspace-scoped on the gateway). */
  disabled?: string[];
}

export interface AutomationRun {
  startedAt?: string;
  completedAt?: string;
  status: string;
  error?: string;
  summary?: string;
  sessionId?: string;
}

export interface AutomationTask {
  id: string;
  title: string;
  prompt: string;
  schedule: string;
  enabled: boolean;
  lastRun?: string;
  nextRun?: string;
  lastError?: string;
  runHistory: AutomationRun[];
}

export interface AutomationInput {
  title: string;
  prompt: string;
  schedule: string;
  enabled?: boolean;
}

export interface InsightsTotals {
  sessions: number;
  turns: number;
  inputTokens: number;
  outputTokens: number;
  toolCalls: number;
}

export interface Insights {
  range: number;
  totals: InsightsTotals;
  byDay: Array<{ day: string; turns: number; toolCalls: number }>;
  /** The backend aggregates by model; it tracks no per-tool split. */
  byModel: Array<{ model: string; calls: number; totalTokens: number }>;
}

async function readJson<T>(res: Response, what: string): Promise<T> {
  if (!res.ok) {
    // The gateway answers `{error:{code,message,data}}`; dropping the message
    // left users with "400 Bad Request" while the server had said exactly what
    // was wrong with the schedule (round-1 tails audit).
    let detail = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: { message?: string } | string };
      const message = typeof body.error === 'string' ? body.error : body.error?.message;
      if (typeof message === 'string' && message.trim() !== '') detail = message;
    } catch {
      // Not JSON: keep the status line.
    }
    throw new Error(`${what} failed: ${detail}`);
  }
  return (await res.json()) as T;
}

export async function loadCapabilities(): Promise<Capabilities> {
  const res = await authedFetch('/api/capabilities', { headers: { Accept: 'application/json' } });
  return readJson<Capabilities>(res, 'capabilities');
}

export async function loadAutomation(): Promise<AutomationTask[]> {
  const res = await authedFetch('/api/automation', { headers: { Accept: 'application/json' } });
  const body = await readJson<{ tasks: AutomationTask[] }>(res, 'automation');
  return body.tasks;
}

export async function updateAutomation(
  id: string,
  patch: Partial<AutomationInput>,
): Promise<AutomationTask> {
  const res = await authedFetch(`/api/automation/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(patch),
  });
  const body = await readJson<{ task: AutomationTask }>(res, 'automation update');
  return body.task;
}

export async function createAutomation(input: AutomationInput): Promise<AutomationTask> {
  const res = await authedFetch('/api/automation', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(input),
  });
  const body = await readJson<{ task: AutomationTask }>(res, 'automation create');
  return body.task;
}

export async function deleteAutomation(id: string): Promise<void> {
  const res = await authedFetch(`/api/automation/${encodeURIComponent(id)}`, { method: 'DELETE' });
  await readJson<{ ok: boolean }>(res, 'automation delete');
}

export async function runAutomationNow(id: string): Promise<void> {
  const res = await authedFetch(`/api/automation/${encodeURIComponent(id)}/run`, { method: 'POST' });
  await readJson<{ ok: boolean }>(res, 'automation run');
}

/** Kept for the switch: it is an update of one field. */
export async function setAutomationEnabled(id: string, enabled: boolean): Promise<void> {
  await updateAutomation(id, { enabled });
}

export async function loadInsights(range: 7 | 30): Promise<Insights> {
  const res = await authedFetch(`/api/insights?range=${range}`, {
    headers: { Accept: 'application/json' },
  });
  return readJson<Insights>(res, 'insights');
}
/** One row of the session Memory tab. The store is workspace-scoped and this build
 * has one fixed workspace, so the tab shows that whole memory. */
export interface MemoryEntry {
  id: string;
  title: string;
  summary?: string;
  type?: string;
  updatedAt?: string;
}

export async function loadMemory(sessionId: string): Promise<MemoryEntry[]> {
  const res = await authedFetch(`/api/sessions/${encodeURIComponent(sessionId)}/memory`, {
    headers: { Accept: 'application/json' },
  });
  const body = await readJson<{ entries: MemoryEntry[] }>(res, 'session memory');
  return body.entries;
}

/** One model the composer can switch to (GET /api/models). */
export interface ModelThinking {
  id: string;
  nameZh: string;
  nameEN: string;
}

export interface ModelRow {
  id: string;
  name: string;
  note?: string;
  contextWindow?: number;
  thinkingModes: ModelThinking[];
}

export interface ModelsView {
  models: ModelRow[];
  defaultModel?: string;
}

export async function loadModels(): Promise<ModelsView> {
  const res = await authedFetch('/api/models', { headers: { Accept: 'application/json' } });
  const body = await readJson<ModelsView>(res, 'models');
  return { models: body.models ?? [], defaultModel: body.defaultModel };
}
