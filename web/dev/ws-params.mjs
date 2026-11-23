/**
 * The WS method → params contract, in one place.
 *
 * Why this exists: the mock used to accept any extra field and silently ignore
 * missing ones, so a client that sent `{text}` where the gateway reads
 * `{prompt}` looked healthy in 122 tests and failed on the real gateway with
 * `invalid_params`. Remembering that rule is not a mechanism; a shared table
 * that both the mock and the fake gateway validate against is.
 *
 * Field names are copied from the gateway's own structs
 * (`internal/gateway/methods.go`: `promptsEnqueueParams.Prompt`,
 * `sessionSnapshotParams.Tail`, …). When the Go side adds a method, add it here
 * too — `alignment.test.ts` fails if the two drift apart.
 */

/** @typedef {{ allowed: string[], required?: string[] }} MethodParams */

/** @type {Record<string, MethodParams>} */
export const WS_PARAMS = {
  initialize: {
    allowed: ['clientName', 'clientVersion', 'protocolVersion', 'capabilities'],
    required: ['protocolVersion'],
  },
  ready: { allowed: [] },
  ping: { allowed: [] },

  'session.list.snapshot': { allowed: [] },
  'session.snapshot': {
    allowed: ['sessionId', 'tail', 'beforeMessageId'],
    required: ['sessionId'],
  },
  'session.subscribe': { allowed: ['sessionId', 'sinceRev'], required: ['sessionId'] },
  'session.unsubscribe': { allowed: ['sessionId'], required: ['sessionId'] },
  'session.create': { allowed: ['title'] },
  'session.active.get': { allowed: [] },

  'approvals.snapshot': { allowed: [] },
  'approvals.subscribe': { allowed: ['sinceRev'] },
  'approval.resolve': { allowed: ['id', 'decision'], required: ['id', 'decision'] },

  'ask.snapshot': { allowed: [] },
  'ask.subscribe': { allowed: ['sinceRev'] },
  'ask.answer': { allowed: ['id', 'answer'], required: ['id', 'answer'] },
  'ask.cancel': { allowed: ['id'], required: ['id'] },

  'plan.snapshot': { allowed: [] },
  'plan.subscribe': { allowed: ['sinceRev'] },
  'plan.exit': { allowed: ['action'], required: ['action'] },

  'artifacts.snapshot': { allowed: [] },
  'artifacts.subscribe': { allowed: ['sinceRev'] },

  'settings.snapshot': { allowed: [] },
  'settings.subscribe': { allowed: ['sinceRev'] },
  'settings.set': { allowed: ['key', 'value'], required: ['key'] },

  'workspace.list.snapshot': { allowed: [] },

  'context.snapshot': { allowed: ['sessionId', 'sinceRev'], required: ['sessionId'] },
  'context.subscribe': { allowed: ['sessionId', 'sinceRev'], required: ['sessionId'] },
  'tasks.snapshot': { allowed: ['sessionId', 'sinceRev'], required: [] },
  'tasks.subscribe': { allowed: ['sessionId', 'sinceRev'], required: [] },
  'task.status': { allowed: ['id', 'status', 'sessionId'], required: ['id', 'status'] },
  'task.delete': { allowed: ['id', 'sessionId'], required: ['id'] },
  'prompts.snapshot': { allowed: ['sessionId'], required: ['sessionId'] },
  'prompts.subscribe': { allowed: ['sessionId', 'sinceRev'], required: ['sessionId'] },
  'prompts.enqueue': { allowed: ['sessionId', 'prompt'], required: ['sessionId', 'prompt'] },
  'prompts.remove': { allowed: ['sessionId', 'id'], required: ['sessionId', 'id'] },

  'run.cancel': { allowed: ['sessionId'], required: ['sessionId'] },
  'foreground.set': { allowed: ['sessionId'] },
  'notify.action': { allowed: ['id', 'actionId'], required: ['id'] },
};

/**
 * Check one request's params against the table.
 * @returns {{ ok: true } | { ok: false, field: string, reason: string }}
 */
export function validateParams(method, params) {
  const rule = WS_PARAMS[method];
  if (!rule) return { ok: true }; // unknown methods are the dispatcher's problem
  const object = params ?? {};
  if (typeof object !== 'object' || Array.isArray(object)) {
    return { ok: false, field: '', reason: 'params must be an object' };
  }
  for (const key of Object.keys(object)) {
    if (!rule.allowed.includes(key)) {
      return { ok: false, field: key, reason: 'unknown parameter' };
    }
  }
  for (const key of rule.required ?? []) {
    if (object[key] === undefined || object[key] === null) {
      return { ok: false, field: key, reason: 'is required' };
    }
  }
  return { ok: true };
}