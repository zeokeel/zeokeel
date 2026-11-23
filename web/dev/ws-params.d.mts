/** Types for the shared WS params table (see ws-params.mjs). */
export type MethodParams = { allowed: string[]; required?: string[] };
export declare const WS_PARAMS: Record<string, MethodParams>;
export type ParamsCheck =
  | { ok: true }
  | { ok: false; field: string; reason: string };
export declare function validateParams(method: string, params: unknown): ParamsCheck;
