/** Read models for the remaining screens: capabilities (R23), automation (R22), insights (R24). */

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
  status: string;
}

export interface Capabilities {
  skills: SkillSummary[];
  connectors: ConnectorSummary[];
}

export interface AutomationTask {
  id: string;
  title: string;
  schedule: string;
  enabled: boolean;
  lastRun?: string;
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
  byDay: Array<{ day: string; turns: number }>;
  byTool: Array<{ tool: string; calls: number }>;
}

async function readJson<T>(res: Response, what: string): Promise<T> {
  if (!res.ok) throw new Error(`${what} failed: ${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}

export async function loadCapabilities(): Promise<Capabilities> {
  const res = await fetch('/api/capabilities', { headers: { Accept: 'application/json' } });
  return readJson<Capabilities>(res, 'capabilities');
}

export async function loadAutomation(): Promise<AutomationTask[]> {
  const res = await fetch('/api/automation', { headers: { Accept: 'application/json' } });
  const body = await readJson<{ tasks: AutomationTask[] }>(res, 'automation');
  return body.tasks;
}

export async function setAutomationEnabled(id: string, enabled: boolean): Promise<void> {
  const res = await fetch(`/api/automation/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ enabled }),
  });
  await readJson<{ task: AutomationTask }>(res, 'automation update');
}

export async function loadInsights(range: 7 | 30): Promise<Insights> {
  const res = await fetch(`/api/insights?range=${range}`, {
    headers: { Accept: 'application/json' },
  });
  return readJson<Insights>(res, 'insights');
}