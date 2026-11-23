#!/usr/bin/env node
// The WS parameter table, derived from the generated contract.
//
// `src/design/ws-contract.ts` is generated from `internal/gateway/contract.json`
// (the gateway owns it). This module turns it into the runtime shape the mock and
// the fake gateway validate with — no hand-written method list, because every
// hand-written copy was a place to drift:
//
//   - `prompts.enqueue` was accepted by the mock as `text` while the gateway
//     wanted `prompt` (two integration bugs);
//   - `settings.set`'s `value` carries `omitempty`, so it is *optional* in the
//     contract even though the client always sends it. `required` here means
//     "no omitempty in the Go struct", nothing more.
//
import { WS_PARAMS as CONTRACT_PARAMS } from '../src/design/ws-contract.ts';

/** @typedef {{ allowed: string[], required?: string[] }} MethodParams */

/** @type {Record<string, MethodParams>} */
export const WS_PARAMS = Object.fromEntries(
  Object.entries(CONTRACT_PARAMS).map(([method, params]) => [
    method,
    { allowed: [...params.required, ...params.optional], required: [...params.required] },
  ]),
);

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